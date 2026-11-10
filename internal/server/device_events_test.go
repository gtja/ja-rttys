package server

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestDeviceChangesLifecycleAndIsolation(t *testing.T) {
	srv := New(Config{})
	a, stopA := srv.deviceChanges.subscribe("a")
	b, stopB := srv.deviceChanges.subscribe("b")
	defer stopA()
	defer stopB()
	dev := &Device{id: "device", group: "a"}
	if !srv.AddDevice(dev) {
		t.Fatal("registration failed")
	}
	select {
	case <-a:
	default:
		t.Fatal("online device did not notify the open list")
	}
	select {
	case <-b:
		t.Fatal("another tenant received the event")
	default:
	}
	if srv.AddDevice(dev) {
		t.Fatal("duplicate registration accepted")
	}
	srv.DelDevice(&Device{id: dev.id, group: dev.group})
	select {
	case <-a:
		t.Fatal("failed/stale operations published a false change")
	default:
	}
	srv.DelDevice(dev)
	select {
	case <-a:
	default:
		t.Fatal("offline device did not notify the open list")
	}
	for i := 0; i < 1000; i++ {
		srv.deviceChanges.publish("a")
	}
	if len(a) != 1 {
		t.Fatal("slow reader queue was not coalesced")
	}
	stopA()
	stopB()
	if len(srv.deviceChanges.subscribers) != 0 {
		t.Fatal("disconnected subscriptions leaked")
	}
}

func TestDeviceEventsAuthenticatedStream(t *testing.T) {
	a := securityAuth(t)
	a.srv = New(Config{})
	a.srv.tenant = a
	_, err := a.db.Exec(`CREATE TABLE tenants(id TEXT, disabled INTEGER);
CREATE TABLE members(tenant TEXT, user TEXT, role TEXT);
INSERT INTO tenants VALUES('a',0),('b',0);
INSERT INTO sessions VALUES('` + digest("test-session") + `','1','a',9999999999);`)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	a.routes(r)
	s := httptest.NewServer(r)
	defer s.Close()
	request := func(cookie, origin string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", s.URL+"/api/events/devices", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: tenantSessionCookie, Value: cookie})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	if response := request("", ""); response.StatusCode != 401 {
		t.Fatalf("anonymous stream status: %d", response.StatusCode)
	}
	if response := request("test-session", "https://elsewhere.test"); response.StatusCode != 403 {
		t.Fatalf("cross-origin stream status: %d", response.StatusCode)
	}
	response := request("test-session", "")
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("unexpected stream headers: %d %v", response.StatusCode, response.Header)
	}
	events := make(chan string, 8)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "event: ") {
				events <- strings.TrimPrefix(line, "event: ")
			}
		}
	}()
	next := func() string {
		t.Helper()
		select {
		case event := <-events:
			return event
		case <-time.After(2 * time.Second):
			t.Fatal("stream event timed out")
			return ""
		}
	}
	if next() != "devices-changed" {
		t.Fatal("connection did not request initial resynchronization")
	}
	a.srv.deviceChanges.publish("b")
	select {
	case event := <-events:
		t.Fatalf("tenant b leaked event to tenant a: %s", event)
	case <-time.After(50 * time.Millisecond):
	}
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dev := &Device{id: "device", group: "a", conn: serverConn, ctx: ctx, cancel: cancel}
	if !a.srv.AddDevice(dev) || next() != "devices-changed" {
		t.Fatal("online notification missing")
	}
	a.srv.DelDevice(dev)
	if next() != "devices-changed" {
		t.Fatal("offline notification missing")
	}
	// A stream must stop when the session changes tenant, even if the cookie
	// remains valid. Data APIs will likewise re-check the session on refresh.
	a.mu.Lock()
	_, err = a.db.Exec("UPDATE sessions SET tenant='b'")
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.srv.deviceChanges.publish("a")
	if next() != "" {
		t.Fatal("old tenant stream survived a tenant switch")
	}
	// A reconnect takes its scope from the updated session, not a query string.
	reconnected := request("test-session", "")
	if reconnected.StatusCode != 200 {
		t.Fatal("reconnect failed")
	}
	reconnected.Body.Close()
}

package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/zhaojh329/rtty-go/proto"
	"golang.org/x/crypto/bcrypt"
)

func TestSecurityRegistrationEmptyHeartbeat(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("untrusted registration panicked: %v", r)
		}
	}()
	dev := &Device{}
	if err := dev.ParseRegister([]byte{5, proto.MsgRegAttrHeartbeat, 0, 0}); err == nil {
		t.Fatal("accepted empty heartbeat")
	}
}

func TestSecurityShortDeviceFrames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(*Device, []byte) error
		min     int
	}{
		{"logout", handleLogoutMsg, 32}, {"login", handleLoginMsg, 33},
		{"terminal", handleTermDataMsg, 32}, {"file", handleFileMsg, 33},
		{"http", handleHttpMsg, 18}, {"serial ports", handleSerialPortsMsg, 33},
		{"serial open", handleSerialOpenMsg, 33},
	} {
		for n := 0; n < tc.min; n++ {
			t.Run(fmt.Sprintf("%s/%d", tc.name, n), func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("short frame panicked: %v", r)
					}
				}()
				if err := tc.handler(&Device{}, make([]byte, n)); err == nil {
					t.Fatal("accepted short frame")
				}
			})
		}
	}
}

func TestSecurityHeartbeatAndCommand(t *testing.T) {
	for n := 0; n < 4; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("heartbeat panicked: %v", r)
				}
			}()
			data := append([]byte{proto.MsgHeartbeatAttrUptime, 0, byte(n)}, make([]byte, n)...)
			if parseHeartbeat(&Device{proto: 5}, data) {
				t.Fatal("accepted short uptime")
			}
		})
	}
	for _, data := range []string{`{"attrs":null}`, `{}`, `null`} {
		t.Run(data, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("command panicked: %v", r)
				}
			}()
			if err := handleCmdMsg(&Device{}, []byte(data)); err == nil {
				t.Fatal("accepted missing command attributes")
			}
		})
	}
}

func TestSecurityFileMessageEscaping(t *testing.T) {
	for _, name := range []string{`x", "type": "recvfile`, `a"b`, "a\nb", `../../file.txt`, `C:\temp\file.txt`} {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				(&User{conn: conn}).OnFile(append([]byte{proto.MsgTypeFileSend}, []byte(name)...))
			}))
			defer s.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(time.Second))
			typ, data, err := conn.ReadMessage()
			if err != nil || typ != websocket.TextMessage {
				t.Fatalf("read: %v", err)
			}
			var msg struct {
				Type string
				Name string
			}
			if err = json.Unmarshal(data, &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Type != "sendfile" {
				t.Fatalf("injected message type: %s", data)
			}
			if msg.Name == "" || strings.ContainsAny(msg.Name, "/\\\n\r") {
				t.Fatalf("unsafe filename: %q", msg.Name)
			}
		})
	}
}

func securityAuth(t *testing.T) *TenantAuth {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE users(id TEXT, username TEXT, password BLOB, admin INTEGER, disabled INTEGER DEFAULT 0); CREATE TABLE sessions(hash TEXT,user TEXT,tenant TEXT,expires INTEGER)`); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("Correct-test-password!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO users(id,username,password,admin) VALUES('1','admin',?,1)", hash); err != nil {
		t.Fatal(err)
	}
	return &TenantAuth{db: db, attempts: map[string]loginAttempt{}, passwordSlots: make(chan struct{}, 4)}
}

func securitySignin(a *TenantAuth, username, password, ip string) (*httptest.ResponseRecorder, time.Duration) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "https://console.test/api/signin", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = ip + ":1234"
	start := time.Now()
	a.signin(c)
	c.Writer.WriteHeaderNow()
	return w, time.Since(start)
}

func TestSecuritySigninTiming(t *testing.T) {
	a := securityAuth(t)
	known, slow := securitySignin(a, "admin", "wrong", "192.0.2.1")
	missing, fast := securitySignin(a, "does-not-exist", "wrong", "192.0.2.1")
	t.Logf("known=%s missing=%s", slow, fast)
	if known.Code != 401 || missing.Code != 401 {
		t.Fatal("different failure responses")
	}
	if fast < slow/2 {
		t.Fatal("missing username skipped password work")
	}
}

func TestSecuritySigninCapacityAndIsolation(t *testing.T) {
	a := securityAuth(t)
	for n := 0; n < 20001; n++ {
		a.attempts[fmt.Sprint(n)] = loginAttempt{Until: time.Now().Add(time.Minute)}
	}
	w, _ := securitySignin(a, "admin", "Correct-test-password!", "192.0.2.2")
	if w.Code != 200 {
		t.Fatalf("unrelated entries blocked legitimate login: %d", w.Code)
	}
	if len(a.attempts) > 20000 {
		t.Fatalf("unbounded attempts: %d", len(a.attempts))
	}
}

func TestSecuritySigninSourceIsolation(t *testing.T) {
	a := securityAuth(t)
	for n := 0; n < 10; n++ {
		w, _ := securitySignin(a, "admin", "wrong", "192.0.2.1")
		if w.Code != 401 {
			t.Fatalf("attempt %d: %d", n, w.Code)
		}
	}
	w, _ := securitySignin(a, "admin", "wrong", "192.0.2.1")
	if w.Code != 429 {
		t.Fatalf("expected throttle, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
	w, _ = securitySignin(a, "admin", "Correct-test-password!", "192.0.2.2")
	if w.Code != 200 {
		t.Fatalf("other source locked out: %d", w.Code)
	}
}

func TestSecuritySigninCookie(t *testing.T) {
	a := securityAuth(t)
	w, _ := securitySignin(a, "admin", "Correct-test-password!", "192.0.2.1")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %v", cookies)
	}
	c := cookies[0]
	if c.Name != "__Host-rt_sid" || c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %s", c)
	}
}

func TestSecuritySigninBoundedWindow(t *testing.T) {
	a := &TenantAuth{attempts: map[string]loginAttempt{}}
	now := time.Unix(1700000000, 0)
	for n := 0; n < 10; n++ {
		if retry := a.limitLoginLocked("ip", "account", now); retry != 0 {
			t.Fatal("premature throttle")
		}
	}
	if retry := a.limitLoginLocked("ip", "account", now.Add(30*time.Second)); retry != 30*time.Second {
		t.Fatalf("unexpected remaining window: %s", retry)
	}
	if retry := a.limitLoginLocked("ip", "account", now.Add(time.Minute)); retry != 0 {
		t.Fatalf("window did not expire: %s", retry)
	}
	for n := 0; n < 100; n++ {
		if retry := a.limitLoginLocked("other-ip", fmt.Sprint(n), now); retry != 0 {
			t.Fatal("premature source throttle")
		}
	}
	if retry := a.limitLoginLocked("other-ip", "another-account", now); retry != time.Minute {
		t.Fatalf("aggregate source limit absent: %s", retry)
	}
}

func TestSecurityDeviceConnectionIsolation(t *testing.T) {
	srv := New(Config{Token: "test-device-token"})
	for _, malformedRegistration := range []bool{true, false, false} {
		serverConn, clientConn := net.Pipe()
		clientConn.SetDeadline(time.Now().Add(2 * time.Second))
		done := make(chan struct{})
		go func() { defer close(done); handleDeviceConnection(srv, serverConn) }()
		client := proto.NewMsgReaderWriter(proto.RoleRtty, clientConn)
		registration := append([]byte{3}, []byte("device\x00test\x00test-device-token\x00")...)
		if malformedRegistration {
			registration = []byte{5, proto.MsgRegAttrHeartbeat, 0, 0}
		}
		if err := client.Write(proto.MsgTypeRegister, registration); err != nil {
			t.Fatal(err)
		}
		if malformedRegistration {
			if _, _, err := client.Read(); err == nil {
				t.Fatal("accepted malformed registration")
			}
		} else {
			typ, data, err := client.Read()
			if err != nil || typ != proto.MsgTypeRegister || len(data) == 0 || data[0] != 0 {
				t.Fatalf("healthy connection failed: %d %q %v", typ, data, err)
			}
			if err := client.Write(proto.MsgTypeLogin, []byte{}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := client.Read(); err == nil {
				t.Fatal("accepted malformed post-registration frame")
			}
		}
		clientConn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("malformed device connection leaked")
		}
	}
}

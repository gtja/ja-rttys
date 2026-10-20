// Black-box socket oracle: synthetic device transport, real TLS/HTTP/WebSocket server.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/zhaojh329/rtty-go/proto"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"time"
)

type fixture struct {
	TenantA, TenantB map[string]any
	DeviceA, DeviceB map[string]any
	Users            map[string]map[string]string
	Password         string
}

var checks = []map[string]any{}

func check(name string, ok bool, actual any) {
	checks = append(checks, map[string]any{"name": name, "pass": ok, "actual": actual})
	b, _ := json.MarshalIndent(checks, "", "  ")
	os.WriteFile(os.Getenv("RTTY_SOCKET_EVIDENCE"), b, 0644)
	fmt.Println(name, ok)
	if !ok {
		panic(name)
	}
}
func main() {
	defer func() {
		if v := recover(); v != nil {
			fmt.Fprintln(os.Stderr, v)
			os.Exit(1)
		}
	}()
	run()
}
func run() {
	raw, err := os.ReadFile(os.Getenv("RTTY_FIXTURE"))
	if err != nil {
		panic(err)
	}
	var f struct {
		TenantA  map[string]string            `json:"tenant_a"`
		TenantB  map[string]string            `json:"tenant_b"`
		DeviceA  map[string]string            `json:"device_a"`
		DeviceB  map[string]string            `json:"device_b"`
		Users    map[string]map[string]string `json:"users"`
		Password string                       `json:"password"`
	}
	if json.Unmarshal(raw, &f) != nil {
		panic("fixture")
	}
	base := os.Getenv("RTTY_URL")
	devAddr := os.Getenv("RTTY_DEVICE_ADDR")
	proxyAddr := os.Getenv("RTTY_PROXY_ADDR")
	client := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	call := func(c *http.Client, method, path string, body any) *http.Response {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, r)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", base)
		res, err := c.Do(req)
		if err != nil {
			panic(err)
		}
		return res
	}
	login := func(name, pw string) *http.Client {
		c := client()
		r := call(c, "POST", "/api/signin", map[string]string{"username": name, "password": pw})
		check("socket login "+name, r.StatusCode == 200, r.StatusCode)
		r.Body.Close()
		return c
	}
	a := login(f.Users["alice"]["username"], f.Password)
	b := login(f.Users["bob"]["username"], f.Password)
	admin := login("admin", os.Getenv("RTTY_ADMIN_PASSWORD"))
	register := func(wire, token, group string) (net.Conn, *proto.MsgReaderWriter, byte) {
		conn, e := tls.Dial("tcp", devAddr, &tls.Config{InsecureSkipVerify: true})
		if e != nil {
			panic(e)
		}
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		m := proto.NewMsgReaderWriter(proto.RoleRtty, conn)
		payload := append([]byte{4}, []byte(wire+"\x00fixture\x00"+token+"\x00")...)
		_ = group
		if e = m.Write(proto.MsgTypeRegister, payload); e != nil {
			panic(e)
		}
		_, d, e := m.Read()
		if e != nil {
			panic(e)
		}
		conn.SetDeadline(time.Time{})
		return conn, m, d[0]
	}
	for _, v := range []struct{ wire, token, name string }{{"same-device", "", "missing device secret"}, {"same-device", "wrong-secret-00000000000000000000000000000000", "wrong device secret"}, {"other-device", f.DeviceA["token"], "secret bound to wire identity"}} {
		c, _, code := register(v.wire, v.token, "")
		check(v.name, code != 0, code)
		c.Close()
	}
	ca, ma, code := register("same-device", f.DeviceA["token"], "")
	check("valid A device", code == 0, code)
	defer ca.Close()
	cb, mb, code := register("same-device", f.DeviceB["token"], "")
	check("same wire identity B device", code == 0, code)
	defer cb.Close()
	fake := func(conn net.Conn, m *proto.MsgReaderWriter) {
		var mu sync.Mutex
		write := func(t byte, d ...any) { mu.Lock(); defer mu.Unlock(); m.Write(t, d...) }
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for range ticker.C {
				mu.Lock()
				e := m.Write(proto.MsgTypeHeartbeat, uint32(10))
				mu.Unlock()
				if e != nil {
					return
				}
			}
		}()
		go func() {
			for {
				typ, d, e := m.Read()
				if e != nil {
					return
				}
				switch typ {
				case proto.MsgTypeLogin:
					write(proto.MsgTypeLogin, string(d[:32]), byte(0))
				case proto.MsgTypeTermData:
					write(proto.MsgTypeTermData, d)
				case proto.MsgTypeFile:
					write(proto.MsgTypeFile, d)
				case proto.MsgTypeHttp:
					if len(d) > 25 && bytes.Contains(d[25:], []byte("HTTP/1.1")) {
						src := append([]byte{}, d[1:19]...)
						write(proto.MsgTypeHttp, src, []byte("HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\nPROXY_PROOF"))
						write(proto.MsgTypeHttp, src)
					}
				}
			}
		}()
	}
	fake(ca, ma)
	fake(cb, mb)
	for _, v := range []struct {
		c        *http.Client
		id, name string
	}{{a, f.DeviceA["id"], "A list isolation"}, {b, f.DeviceB["id"], "B list isolation"}} {
		r := call(v.c, "GET", "/api/devs", nil)
		var rows []map[string]any
		json.NewDecoder(r.Body).Decode(&rows)
		r.Body.Close()
		check(v.name, len(rows) == 1 && rows[0]["id"] == v.id, rows)
	}
	wsURL := "wss" + strings.TrimPrefix(base, "https") + "/api/connect/" + f.DeviceA["id"]
	headers := http.Header{}
	req, _ := http.NewRequest("GET", base, nil)
	for _, c := range a.Jar.Cookies(req.URL) {
		headers.Add("Cookie", c.String())
	}
	headers.Set("Origin", base)
	dial := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, HandshakeTimeout: 5 * time.Second}
	bad := headers.Clone()
	bad.Set("Origin", "https://foreign.example")
	_, resp, e := dial.Dial(wsURL, bad)
	check("foreign WS Origin rejected", e != nil && resp != nil && resp.StatusCode == 403, "denied")
	ws, _, e := dial.Dial(wsURL, headers)
	if e != nil {
		panic(e)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, e := ws.ReadMessage()
	check("terminal login", e == nil && bytes.Contains(data, []byte("login")), string(data))
	ws.WriteMessage(websocket.BinaryMessage, append([]byte{0}, []byte("TERMINAL_PROOF")...))
	_, data, e = ws.ReadMessage()
	check("terminal data roundtrip", e == nil && bytes.Contains(data, []byte("TERMINAL_PROOF")), string(data))
	ws.WriteMessage(websocket.BinaryMessage, []byte{1, proto.MsgTypeFileData, 7, 8, 9})
	_, data, e = ws.ReadMessage()
	check("file channel roundtrip", e == nil && bytes.Equal(data, []byte{1, 7, 8, 9}), data)
	r := call(a, "GET", "/api/web/"+f.DeviceA["id"]+"/http/127.0.0.1:9999/", nil)
	check("unlisted proxy target denied", r.StatusCode == 403, r.StatusCode)
	r.Body.Close()
	r = call(a, "GET", "/api/web/"+f.DeviceA["id"]+"/http/127.0.0.1:8080/", nil)
	check("proxy ticket issued", r.StatusCode == 302, r.StatusCode)
	ticketURL := r.Header.Get("Location")
	r.Body.Close()
	jar, _ := cookiejar.New(nil)
	pc := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
	}}}
	r, e = pc.Get(ticketURL)
	if e != nil {
		panic(e)
	}
	data, _ = io.ReadAll(r.Body)
	r.Body.Close()
	proxyURL := r.Request.URL.String()
	check("proxy data roundtrip", r.StatusCode == 200 && string(data) == "PROXY_PROOF", string(data))
	// A public proxy hostname must never reveal its bearer cookie.
	attack, _ := http.NewRequest("GET", proxyURL, nil)
	attack.Header.Set("Cookie", "rt_proxy="+strings.Split(attack.URL.Hostname(), ".")[0])
	attacker := &http.Client{Transport: pc.Transport, Timeout: 5 * time.Second}
	r, e = attacker.Do(attack)
	if e != nil {
		panic(e)
	}
	check("public proxy hostname is not a credential", r.StatusCode == 401, r.StatusCode)
	r.Body.Close()
	r = call(admin, "POST", "/api/tenant", map[string]string{"tenant_id": f.TenantA["id"]})
	check("admin switch A", r.StatusCode == 200, r.StatusCode)
	r.Body.Close()
	r = call(admin, "DELETE", "/api/manage/members/"+f.Users["alice"]["id"], nil)
	check("revoke active owner membership", r.StatusCode == 200, r.StatusCode)
	r.Body.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, e = ws.ReadMessage()
	check("existing WS closes on revoke", e != nil, fmt.Sprint(e))
	r, e = pc.Get(proxyURL)
	if e != nil {
		panic(e)
	}
	check("existing proxy revoked", r.StatusCode == 401, r.StatusCode)
	r.Body.Close()
	r = call(a, "GET", "/api/devs", nil)
	check("revoked login rejects API", r.StatusCode == 401, r.StatusCode)
	r.Body.Close()
	r = call(admin, "POST", "/api/manage/devices/"+f.DeviceA["id"]+"/rotate", map[string]string{})
	check("rotate device secret", r.StatusCode == 200, r.StatusCode)
	r.Body.Close()
	time.Sleep(200 * time.Millisecond)
	c, _, code := register("same-device", f.DeviceA["token"], "")
	check("old device secret rejected", code != 0, code)
	c.Close()
	for i := 0; i < 10; i++ {
		r = call(admin, "POST", "/api/signin", map[string]string{"username": "admin", "password": "intentionally-wrong"})
		r.Body.Close()
		r = call(b, "POST", "/api/signin", map[string]string{"username": f.Users["bob"]["username"], "password": f.Password})
		check("valid alternate login", r.StatusCode == 200, r.StatusCode)
		r.Body.Close()
	}
	r = call(admin, "POST", "/api/signin", map[string]string{"username": "admin", "password": "intentionally-wrong"})
	check("own login cannot reset another account throttle", r.StatusCode == 429, r.StatusCode)
	r.Body.Close()
}

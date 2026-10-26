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
	"sync/atomic"
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
	var downloadSent atomic.Int32
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
			fileIndex, fileTotal, fileNext := 0, 0, false
			fileSID := ""
			for {
				typ, d, e := m.Read()
				if e != nil {
					return
				}
				switch typ {
				case proto.MsgTypeLogin:
					write(proto.MsgTypeLogin, string(d[:32]), byte(0))
				case proto.MsgTypeTermData:
					if len(d) > 32 && strings.HasPrefix(string(d[32:]), "__window_") {
						fileSID = string(d[:32])
						fileIndex = 0
						fileTotal = 40
						fileNext = false
						downloadSent.Store(0)
						if strings.Contains(string(d[32:]), "small") || strings.Contains(string(d[32:]), "pair") {
							fileTotal = 2
						}
						fileNext = strings.Contains(string(d[32:]), "pair")
						write(proto.MsgTypeFile, fileSID, proto.MsgTypeFileSend, []byte("window.bin"))
						continue
					}
					write(proto.MsgTypeTermData, d)
				case proto.MsgTypeFile:
					if len(d) > 32 && string(d[:32]) == fileSID {
						if d[32] == proto.MsgTypeFileAbort {
							fileSID = ""
							continue
						}
						if d[32] == proto.MsgTypeFileAck {
							if fileIndex < fileTotal {
								fileIndex++
								downloadSent.Add(1)
								write(proto.MsgTypeFile, fileSID, proto.MsgTypeFileData, bytes.Repeat([]byte{byte(fileIndex)}, 16))
							} else {
								write(proto.MsgTypeFile, fileSID, proto.MsgTypeFileData)
								if fileNext {
									fileNext = false
									fileIndex = 0
									write(proto.MsgTypeFile, fileSID, proto.MsgTypeFileSend, []byte("next.bin"))
								} else {
									fileSID = ""
								}
							}
							continue
						}
					}
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
		check("actual registration description exposed", rows[0]["client_description"] == "fixture", rows[0]["client_description"])
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
	// Bounded download window and transfer-ID lifecycle, over real WebSocket/TLS.
	startFile := func(w *websocket.Conn, command string) string {
		w.WriteMessage(websocket.BinaryMessage, append([]byte{0}, []byte(command)...))
		w.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := w.ReadMessage()
		if err != nil {
			panic(err)
		}
		var meta map[string]any
		json.Unmarshal(msg, &meta)
		id, _ := meta["transfer_id"].(string)
		check("download has transfer identity", meta["type"] == "sendfile" && id != "", meta)
		return id
	}
	windowID := startFile(ws, "__window_large__")
	ws.WriteJSON(map[string]any{"type": "fileStart", "transfer_id": windowID, "window": 16})
	for i := 0; i < 16; i++ {
		_, chunk, err := ws.ReadMessage()
		check("window ordered chunk", err == nil && bytes.Equal(chunk, append([]byte{1}, bytes.Repeat([]byte{byte(i + 1)}, 16)...)), i)
	}
	time.Sleep(100 * time.Millisecond)
	check("download window bounded to16", downloadSent.Load() == 16, downloadSent.Load())
	ws.WriteJSON(map[string]any{"type": "fileCanceled", "transfer_id": windowID})
	ws.WriteMessage(websocket.BinaryMessage, []byte{0, 'C', 'A', 'N', 'C', 'E', 'L'})
	_, data, e = ws.ReadMessage()
	check("cancel preserves terminal", e == nil && bytes.Contains(data, []byte("CANCEL")), string(data))
	pairID := startFile(ws, "__window_pair__")
	for fileNo := 0; fileNo < 2; fileNo++ {
		ws.WriteJSON(map[string]any{"type": "fileStart", "transfer_id": pairID, "window": 16})
		for i := 0; i < 2; i++ {
			_, chunk, err := ws.ReadMessage()
			check("consecutive file intact", err == nil && bytes.Equal(chunk, append([]byte{1}, bytes.Repeat([]byte{byte(i + 1)}, 16)...)), i)
			ws.WriteJSON(map[string]any{"type": "fileAck", "transfer_id": pairID})
		}
		_, chunk, err := ws.ReadMessage()
		check("consecutive file EOF", err == nil && bytes.Equal(chunk, []byte{1}), chunk)
		if fileNo == 0 {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				panic(err)
			}
			var meta map[string]any
			json.Unmarshal(msg, &meta)
			oldID := pairID
			pairID, _ = meta["transfer_id"].(string)
			check("next file has new identity", pairID != "" && pairID != oldID, pairID)
			ws.WriteJSON(map[string]any{"type": "fileAck", "transfer_id": oldID})
			ws.WriteJSON(map[string]any{"type": "fileCanceled", "transfer_id": oldID})
		}
	}
	dropped, _, err := dial.Dial(wsURL, headers)
	if err != nil {
		panic(err)
	}
	dropped.SetReadDeadline(time.Now().Add(5 * time.Second))
	dropped.ReadMessage()
	dropID := startFile(dropped, "__window_large__")
	dropped.WriteJSON(map[string]any{"type": "fileStart", "transfer_id": dropID, "window": 16})
	dropped.ReadMessage()
	dropped.Close()
	reconnected, _, err := dial.Dial(wsURL, headers)
	if err != nil {
		panic(err)
	}
	defer reconnected.Close()
	reconnected.SetReadDeadline(time.Now().Add(5 * time.Second))
	reconnected.ReadMessage()
	id := startFile(reconnected, "__window_small__")
	reconnected.WriteJSON(map[string]any{"type": "fileStart", "transfer_id": id, "window": 16})
	for i := 0; i < 2; i++ {
		_, chunk, err := reconnected.ReadMessage()
		check("reconnect download intact", err == nil && len(chunk) == 17, i)
		reconnected.WriteJSON(map[string]any{"type": "fileAck", "transfer_id": id})
	}
	_, data, e = reconnected.ReadMessage()
	check("reconnect EOF", e == nil && bytes.Equal(data, []byte{1}), data)
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

	// Multi-group policy oracle: real channels must survive any remaining grant.
	mutate := func(name, method, path string, body any, expected int) {
		r := call(admin, method, path, body)
		check(name, r.StatusCode == expected, r.StatusCode)
		r.Body.Close()
	}
	r = call(admin, "GET", "/api/manage/snapshot", nil)
	var snapshot map[string]any
	json.NewDecoder(r.Body).Decode(&snapshot)
	r.Body.Close()
	check("legacy group membership preserved", bytes.Contains(mustJSON(snapshot["device_groups"]), []byte("lab")), snapshot["device_groups"])
	mutate("create group project", "POST", "/api/manage/groups", map[string]string{"name": "project"}, 201)
	mutate("create group maintenance", "POST", "/api/manage/groups", map[string]string{"name": "maintenance"}, 201)
	mutate("duplicate group denied", "POST", "/api/manage/groups", map[string]string{"name": "project"}, 409)
	mutate("restore operator membership", "PUT", "/api/manage/members", map[string]string{"user_id": f.Users["operator"]["id"], "role": "operator"}, 200)
	op := login(f.Users["operator"]["username"], f.Password)
	r = call(op, "POST", "/api/manage/groups", map[string]string{"name": "illegal"})
	check("operator cannot manage groups", r.StatusCode == 403, r.StatusCode)
	r.Body.Close()
	mutate("two memberships", "PUT", "/api/manage/devices/"+f.DeviceA["id"]+"/groups", map[string]any{"groups": []string{"project", "maintenance"}}, 200)
	mutate("foreign device membership denied", "PUT", "/api/manage/devices/"+f.DeviceB["id"]+"/groups", map[string]any{"groups": []string{"project"}}, 404)
	mutate("unknown group rejected atomically", "PUT", "/api/manage/devices/"+f.DeviceA["id"]+"/groups", map[string]any{"groups": []string{"project", "missing"}}, 404)
	grant := func(group, device string, access bool) {
		mutate("grant mutation "+group+device, "PUT", "/api/manage/grants", map[string]any{"user_id": f.Users["operator"]["id"], "group": group, "device_id": device, "access": access}, 200)
	}
	grant("project", "", true)
	grant("maintenance", "", true)
	for _, group := range []string{"project", "maintenance"} {
		r = call(op, "GET", "/api/devs?group="+group, nil)
		var rows []map[string]any
		json.NewDecoder(r.Body).Decode(&rows)
		r.Body.Close()
		check("membership filter "+group, len(rows) == 1 && rows[0]["id"] == f.DeviceA["id"], rows)
	}
	oh := http.Header{"Origin": []string{base}}
	for _, c := range op.Jar.Cookies(req.URL) {
		oh.Add("Cookie", c.String())
	}
	openOperator := func() *websocket.Conn {
		w, _, err := dial.Dial(wsURL, oh)
		if err != nil {
			panic(err)
		}
		w.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := w.ReadMessage()
		check("group authorized terminal opens", err == nil && bytes.Contains(msg, []byte("login")), string(msg))
		return w
	}
	ow := openOperator()
	defer ow.Close()
	r = call(op, "GET", "/api/web/"+f.DeviceA["id"]+"/http/127.0.0.1:8080/", nil)
	check("group proxy issued", r.StatusCode == 302, r.StatusCode)
	oticket := r.Header.Get("Location")
	r.Body.Close()
	r, e = pc.Get(oticket)
	if e != nil {
		panic(e)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	opProxy := r.Request.URL.String()
	alive := func(label string) {
		ow.SetReadDeadline(time.Now().Add(5 * time.Second))
		ow.WriteMessage(websocket.BinaryMessage, []byte{1, proto.MsgTypeFileData, 4, 5, 6})
		_, msg, err := ow.ReadMessage()
		check(label+" file remains", err == nil && bytes.Equal(msg, []byte{1, 4, 5, 6}), msg)
		ow.WriteMessage(websocket.BinaryMessage, []byte{0, 'O', 'K'})
		_, msg, err = ow.ReadMessage()
		check(label+" terminal remains", err == nil && bytes.Equal(msg, []byte{0, 'O', 'K'}), msg)
		r, err := pc.Get(opProxy)
		if err != nil {
			panic(err)
		}
		check(label+" proxy remains", r.StatusCode == 200, r.StatusCode)
		r.Body.Close()
	}
	denied := func(label string) {
		ow.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, err := ow.ReadMessage()
		check(label+" existing channel closed", err != nil, fmt.Sprint(err))
		r, err := pc.Get(opProxy)
		if err != nil {
			panic(err)
		}
		check(label+" proxy revoked", r.StatusCode == 401, r.StatusCode)
		r.Body.Close()
		_, res, err := dial.Dial(wsURL, oh)
		check(label+" new channel denied", err != nil && res != nil && res.StatusCode == 403, "403 expected")
	}
	grant("project", "", false)
	alive("remaining group")
	mutate("rename granted group", "PATCH", "/api/manage/groups/maintenance", map[string]string{"name": "renamed"}, 200)
	alive("renamed group")
	grant("", f.DeviceA["id"], true)
	mutate("remove all group memberships", "PUT", "/api/manage/devices/"+f.DeviceA["id"]+"/groups", map[string]any{"groups": []string{}}, 200)
	alive("direct device grant")
	grant("", f.DeviceA["id"], false)
	denied("last direct grant removed")
	mutate("restore group membership", "PUT", "/api/manage/devices/"+f.DeviceA["id"]+"/groups", map[string]any{"groups": []string{"renamed"}}, 200)
	ow = openOperator()
	defer ow.Close()
	mutate("delete last authorized group", "DELETE", "/api/manage/groups/renamed", nil, 200)
	ow.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, e = ow.ReadMessage()
	check("group delete closes channel", e != nil, fmt.Sprint(e))
	mutate("recreate same name", "POST", "/api/manage/groups", map[string]string{"name": "renamed"}, 201)
	mutate("rejoin recreated group", "PUT", "/api/manage/devices/"+f.DeviceA["id"]+"/groups", map[string]any{"groups": []string{"renamed"}}, 200)
	r = call(op, "GET", "/api/connect/"+f.DeviceA["id"], nil)
	check("deleted grants never resurrect", r.StatusCode == 403, r.StatusCode)
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
	r = call(admin, "POST", "/api/manage/devices/"+f.DeviceA["id"]+"/rotate", map[string]string{"confirm_wire_id": "same-device"})
	check("rotate device secret", r.StatusCode == 200, r.StatusCode)
	r.Body.Close()
	time.Sleep(200 * time.Millisecond)
	c, _, code := register("same-device", f.DeviceA["token"], "")
	check("old device secret rejected", code != 0, code)
	c.Close()
	// Device lifecycle contract: metadata, confirmed rotation, deletion and revocation.
	r = call(admin, "POST", "/api/manage/devices", map[string]string{"wire_id": "delete-device", "name": "friendly label"})
	check("enroll lifecycle device", r.StatusCode == 201, r.StatusCode)
	var life map[string]string
	json.NewDecoder(r.Body).Decode(&life)
	r.Body.Close()
	lc, lm, code := register("delete-device", life["token"], "")
	check("lifecycle first registration", code == 0, code)
	fake(lc, lm)
	defer lc.Close()
	for _, q := range []string{"delete-device", "fixture"} {
		r = call(admin, "GET", "/api/devs?q="+q, nil)
		var rows []map[string]any
		json.NewDecoder(r.Body).Decode(&rows)
		r.Body.Close()
		found := false
		for _, d := range rows {
			if d["id"] == life["id"] && d["client_description"] == "fixture" {
				found = true
			}
		}
		check("search registered identity "+q, found, rows)
	}
	r = call(admin, "POST", "/api/manage/devices/"+life["id"]+"/rotate", map[string]string{})
	check("rotation requires identity confirmation", r.StatusCode == 400, r.StatusCode)
	r.Body.Close()
	r = call(b, "DELETE", "/api/manage/devices/"+life["id"], nil)
	check("foreign tenant cannot soft delete", r.StatusCode == 404, r.StatusCode)
	r.Body.Close()
	r = call(op, "DELETE", "/api/manage/devices/"+life["id"], nil)
	check("operator cannot soft delete", r.StatusCode == 403, r.StatusCode)
	r.Body.Close()
	r = call(admin, "POST", "/api/manage/devices/"+life["id"]+"/rotate", map[string]string{"confirm_wire_id": "delete-device"})
	check("confirmed token reset", r.StatusCode == 200, r.StatusCode)
	var rotated map[string]string
	json.NewDecoder(r.Body).Decode(&rotated)
	r.Body.Close()
	old, _, code := register("delete-device", life["token"], "")
	check("reset rejects old token", code != 0, code)
	old.Close()
	lc.Close()
	time.Sleep(200 * time.Millisecond)
	lc, lm, code = register("delete-device", rotated["token"], "")
	check("reset accepts new token", code == 0, code)
	fake(lc, lm)
	defer lc.Close()
	ah := http.Header{"Origin": []string{base}}
	for _, c := range admin.Jar.Cookies(req.URL) {
		ah.Add("Cookie", c.String())
	}
	lw, _, err := dial.Dial("wss"+strings.TrimPrefix(base, "https")+"/api/connect/"+life["id"], ah)
	if err != nil {
		panic(err)
	}
	defer lw.Close()
	lw.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := lw.ReadMessage()
	check("lifecycle live terminal", err == nil && bytes.Contains(msg, []byte("login")), string(msg))
	r = call(admin, "DELETE", "/api/manage/devices/"+life["id"], nil)
	check("soft delete accepted", r.StatusCode == 200, r.StatusCode)
	r.Body.Close()
	lw.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = lw.ReadMessage()
	check("soft delete closes terminal", err != nil, fmt.Sprint(err))
	old, _, code = register("delete-device", rotated["token"], "")
	check("deleted device cannot reconnect", code != 0, code)
	old.Close()
	r = call(admin, "GET", "/api/dev/"+life["id"], nil)
	check("deleted detail hidden", r.StatusCode == 404, r.StatusCode)
	r.Body.Close()
	r = call(admin, "GET", "/api/manage/snapshot", nil)
	var snap map[string]any
	json.NewDecoder(r.Body).Decode(&snap)
	r.Body.Close()
	check("deleted device absent from inventory", !bytes.Contains(mustJSON(snap["devices"]), []byte(life["id"])), snap["devices"])
	check("soft deletion audited", bytes.Contains(mustJSON(snap["audit"]), []byte("DELETE /api/manage/devices/:id")), "audit event")
	r = call(admin, "PATCH", "/api/manage/devices/"+life["id"], map[string]bool{"disabled": false})
	check("deleted device cannot be enabled", r.StatusCode == 404, r.StatusCode)
	r.Body.Close()
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

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

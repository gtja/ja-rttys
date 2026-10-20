// Real NVIDIA-device acceptance. Credentials and output are external files.
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"
)

var records = []map[string]any{}

func check(n string, ok bool, v any) {
	records = append(records, map[string]any{"name": n, "pass": ok, "actual": v})
	b, _ := json.MarshalIndent(records, "", "  ")
	os.WriteFile(os.Getenv("RTTY_LIVE_EVIDENCE"), b, 0644)
	fmt.Println(n, ok)
	if !ok {
		panic(n)
	}
}
func main() {
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintln(os.Stderr, p)
			os.Exit(1)
		}
	}()
	run()
}
func run() {
	var f struct {
		URL   string `json:"url"`
		Users map[string]struct {
			ID       string `json:"id"`
			Password string `json:"password"`
		} `json:"users"`
		Device map[string]string `json:"device"`
	}
	raw, e := os.ReadFile(os.Getenv("RTTY_ACCESS"))
	if e != nil {
		panic(e)
	}
	if e = json.Unmarshal(raw, &f); e != nil {
		panic(e)
	}
	cert, _ := os.ReadFile(os.Getenv("RTTY_CA"))
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		panic("CA")
	}
	tlsCfg := &tls.Config{RootCAs: pool}
	makeClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	call := func(c *http.Client, m, path string, body any) *http.Response {
		var b io.Reader
		if body != nil {
			v, _ := json.Marshal(body)
			b = bytes.NewReader(v)
		}
		req, _ := http.NewRequest(m, f.URL+path, b)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", f.URL)
		r, e := c.Do(req)
		if e != nil {
			panic(e)
		}
		return r
	}
	login := func(user string) *http.Client {
		c := makeClient()
		r := call(c, "POST", "/api/signin", map[string]string{"username": user, "password": f.Users[user].Password})
		check("login "+user, r.StatusCode == 200, r.StatusCode)
		r.Body.Close()
		return c
	}
	op := login("operator")
	admin := login("tenant-admin")
	outsider := login("outsider")
	r := call(outsider, "GET", "/api/dev/"+f.Device["id"], nil)
	check("real device cross-tenant denied", r.StatusCode == 404, r.StatusCode)
	r.Body.Close()
	r = call(op, "GET", "/api/devs", nil)
	var devices []map[string]any
	json.NewDecoder(r.Body).Decode(&devices)
	r.Body.Close()
	check("real device online", len(devices) == 1 && devices[0]["online"] == true, devices)
	headers := http.Header{}
	rq, _ := http.NewRequest("GET", f.URL, nil)
	for _, c := range op.Jar.Cookies(rq.URL) {
		headers.Add("Cookie", c.String())
	}
	headers.Set("Origin", f.URL)
	ws, _, e := (&websocket.Dialer{TLSClientConfig: tlsCfg}).Dial("wss"+strings.TrimPrefix(f.URL, "https")+"/api/connect/"+f.Device["id"], headers)
	if e != nil {
		panic(e)
	}
	defer ws.Close()
	terminal := ""
	defer func() { os.WriteFile(os.Getenv("RTTY_LIVE_EVIDENCE")+".terminal.log", []byte(terminal), 0600) }()
	read := func() (string, []byte) {
		ws.SetReadDeadline(time.Now().Add(15 * time.Second))
		typ, b, e := ws.ReadMessage()
		if e != nil {
			panic(e)
		}
		if typ == websocket.TextMessage {
			var v map[string]any
			json.Unmarshal(b, &v)
			return fmt.Sprint(v["type"]), b
		}
		if len(b) > 0 && b[0] == 0 {
			terminal += string(b[1:])
			ws.WriteJSON(map[string]any{"type": "ack", "ack": len(b) - 1})
			return "terminal", b[1:]
		}
		return "file", b[1:]
	}
	for {
		kind, _ := read()
		if kind == "login" {
			break
		}
	}
	send := func(s string) {
		if e := ws.WriteMessage(websocket.BinaryMessage, append([]byte{0}, []byte(s)...)); e != nil {
			panic(e)
		}
	}
	send("printf 'RTTY_%s\\n' 'REAL_DEVICE_PROOF'; hostname; uname -m; id\n")
	for !strings.Contains(terminal, "uid=1000(nvidia)") {
		read()
	}
	check("real terminal nvidia account", strings.Contains(terminal, "RTTY_REAL_DEVICE_PROOF") && strings.Contains(terminal, "aarch64"), terminal)
	send("dir=$(mktemp -d /tmp/rttys-e2e.XXXXXX); cd \"$dir\"; /usr/local/bin/rtty-tenant-poc -R; printf 'RTTY_%s\\n' 'UPLOAD_DONE'\n")
	for {
		kind, _ := read()
		if kind == "recvfile" {
			break
		}
	}
	payload := bytes.Repeat([]byte("rttys-tenant-file-proof\x00\xff\n"), 128)
	ws.WriteJSON(map[string]any{"type": "fileInfo", "name": "proof.bin", "size": len(payload)})
	ws.WriteMessage(websocket.BinaryMessage, append([]byte{1, 3}, payload...))
	// The final upload block commits the file without another FileAck. Observe
	// helper completion; the subsequent downloaded bytes are the integrity oracle.
	for !strings.Contains(terminal, "RTTY_UPLOAD_DONE") {
		read()
	}
	send("/usr/local/bin/rtty-tenant-poc -S proof.bin\n")
	downloaded := []byte{}
	started := false
	for {
		kind, data := read()
		if kind == "sendfile" {
			started = true
			ws.WriteJSON(map[string]string{"type": "fileAck"})
		} else if kind == "file" && started {
			if len(data) == 0 {
				break
			}
			downloaded = append(downloaded, data...)
			ws.WriteJSON(map[string]string{"type": "fileAck"})
		}
	}
	hash := sha256.Sum256(downloaded)
	check("real file upload-download", bytes.Equal(payload, downloaded), map[string]any{"bytes": len(downloaded), "sha256": hex.EncodeToString(hash[:])})
	send("rm -f proof.bin; cd /tmp; rmdir \"$dir\"; printf 'RTTY_%s\\n' 'CLEANED'\n")
	for !strings.Contains(terminal, "RTTY_CLEANED") {
		read()
	}
	r = call(op, "GET", "/api/web/"+f.Device["id"]+"/http/127.0.0.1:18083/proof.txt", nil)
	check("real proxy ticket", r.StatusCode == 302, r.StatusCode)
	ticket := r.Header.Get("Location")
	r.Body.Close()
	pc := makeClient()
	pc.CheckRedirect = nil
	r, e = pc.Get(ticket)
	if e != nil {
		panic(e)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	proxyURL := r.Request.URL.String()
	check("real device HTTP proxy", r.StatusCode == 200 && string(b) == "RTTY_WEB_REAL_DEVICE", string(b))
	grant := map[string]any{"user_id": f.Users["operator"].ID, "device_id": f.Device["id"], "access": false}
	defer func() { grant["access"] = true; r := call(admin, "PUT", "/api/manage/grants", grant); r.Body.Close() }()
	r = call(admin, "PUT", "/api/manage/grants", grant)
	check("revoke operator grant", r.StatusCode == 200, r.StatusCode)
	r.Body.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	closed := false
	for i := 0; i < 20; i++ {
		_, _, e = ws.ReadMessage()
		if e != nil {
			closed = true
			break
		}
	}
	check("real existing terminal revoked", closed, fmt.Sprint(e))
	r, e = pc.Get(proxyURL)
	if e != nil {
		panic(e)
	}
	check("real existing proxy revoked", r.StatusCode == 401, r.StatusCode)
	r.Body.Close()
	r = call(op, "GET", "/api/connect/"+f.Device["id"], nil)
	check("new terminal denied after revoke", r.StatusCode == 403, r.StatusCode)
	r.Body.Close()
}

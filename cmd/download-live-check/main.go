// Real NVIDIA-device acceptance. Credentials and output are external files.
package main

import (
	"bytes"
	"crypto/rand"
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
	"regexp"
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
	outsider := login("outsider")
	r := call(outsider, "GET", "/api/dev/"+f.Device["id"], nil)
	check("real device cross-tenant denied", r.StatusCode == 404, r.StatusCode)
	r.Body.Close()
	r = call(op, "GET", "/api/devs", nil)
	filterTarget := func(ds []map[string]any) []map[string]any {
		out := []map[string]any{}
		for _, d := range ds {
			if d["id"] == f.Device["id"] {
				out = append(out, d)
			}
		}
		return out
	}
	var devices []map[string]any
	json.NewDecoder(r.Body).Decode(&devices)
	devices = filterTarget(devices)
	r.Body.Close()
	// Service restart and client reconnect are asynchronous; require bounded readiness.
	deadline := time.Now().Add(60 * time.Second)
	for (len(devices) != 1 || devices[0]["online"] != true) && time.Now().Before(deadline) {
		time.Sleep(time.Second)
		r = call(op, "GET", "/api/devs", nil)
		devices = nil
		json.NewDecoder(r.Body).Decode(&devices)
		devices = filterTarget(devices)
		r.Body.Close()
	}
	check("real device online", len(devices) == 1 && devices[0]["online"] == true, devices)
	check("target is NVIDIA 174", devices[0]["wire_id"] == "nvidia-174" && strings.HasPrefix(fmt.Sprint(devices[0]["ipaddr"]), "172.21.0.174:"), devices[0]["ipaddr"])
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
	payload := make([]byte, 8*1024*1024)
	if _, e := rand.Read(payload); e != nil {
		panic(e)
	}
	ws.WriteJSON(map[string]any{"type": "fileInfo", "name": "proof.bin", "size": len(payload)})
	uploadStart := time.Now()
	sent, inFlight := 0, 0
	pump := func() {
		for sent < len(payload) && inFlight < 16 {
			end := sent + 63*1024
			if end > len(payload) {
				end = len(payload)
			}
			if e := ws.WriteMessage(websocket.BinaryMessage, append([]byte{1, 3}, payload[sent:end]...)); e != nil {
				panic(e)
			}
			sent = end
			inFlight++
		}
	}
	pump()
	for !strings.Contains(terminal, "RTTY_UPLOAD_DONE") {
		kind, _ := read()
		if kind == "fileAck" {
			inFlight--
			pump()
		}
	}
	upSeconds := time.Since(uploadStart).Seconds()
	check("upload throughput window16", sent == len(payload), map[string]any{"bytes": sent, "seconds": upSeconds, "MiB_per_second": float64(sent) / 1048576 / upSeconds})
	send("printf 'RTTY_DIR:%s\\n' \"$dir\"\n")
	pattern := regexp.MustCompile(`RTTY_DIR:(/tmp/rttys-e2e\.[A-Za-z0-9]+)`)
	for pattern.FindStringSubmatch(terminal) == nil {
		read()
	}
	remoteDir := pattern.FindStringSubmatch(terminal)[1]
	lastID := ""
	begin := func(command string) { send(command + "\n") }
	start := func(raw []byte) string {
		var meta map[string]any
		json.Unmarshal(raw, &meta)
		id, _ := meta["transfer_id"].(string)
		check("server negotiates unique transfer", id != "" && id != lastID, meta)
		if lastID != "" {
			ws.WriteJSON(map[string]any{"type": "fileAck", "transfer_id": lastID})
			ws.WriteJSON(map[string]any{"type": "fileCanceled", "transfer_id": lastID})
		}
		lastID = id
		ws.WriteJSON(map[string]any{"type": "fileStart", "transfer_id": id, "window": 16})
		return id
	}
	download := func(label string) {
		started := time.Now()
		data := []byte{}
		id := ""
		for {
			kind, b := read()
			if kind == "sendfile" {
				id = start(b)
			} else if kind == "file" && id != "" {
				if len(b) == 0 {
					break
				}
				data = append(data, b...)
				ws.WriteJSON(map[string]any{"type": "fileAck", "transfer_id": id})
			}
		}
		seconds := time.Since(started).Seconds()
		hash := sha256.Sum256(data)
		check(label, bytes.Equal(data, payload), map[string]any{"bytes": len(data), "seconds": seconds, "MiB_per_second": float64(len(data)) / 1048576 / seconds, "sha256": hex.EncodeToString(hash[:])})
	}
	begin("/usr/local/bin/rtty-tenant-poc -S proof.bin")
	download("window16 real download")
	begin("/usr/local/bin/rtty-tenant-poc -S proof.bin; printf 'RTTY_%s:%s\\n' 'CANCEL_DONE' \"$?\"")
	cancelID := ""
	cancelled := false
	for !strings.Contains(terminal, "RTTY_CANCEL_DONE:") {
		kind, b := read()
		if kind == "sendfile" {
			cancelID = start(b)
		} else if kind == "file" && len(b) > 0 && !cancelled {
			ws.WriteJSON(map[string]any{"type": "fileCanceled", "transfer_id": cancelID})
			cancelled = true
		}
	}
	check("cancel terminates helper without disconnecting terminal", cancelled && !strings.Contains(terminal, "RTTY_CANCEL_DONE:0"), "cancelled helper returned nonzero")
	begin("/usr/local/bin/rtty-tenant-poc -S proof.bin; /usr/local/bin/rtty-tenant-poc -S proof.bin")
	download("consecutive download first")
	download("consecutive download second")
	begin("/usr/local/bin/rtty-tenant-poc -S proof.bin")
	for {
		kind, b := read()
		if kind == "sendfile" {
			start(b)
		} else if kind == "file" && len(b) > 0 {
			break
		}
	}
	ws.Close()
	ws, _, e = (&websocket.Dialer{TLSClientConfig: tlsCfg}).Dial("wss"+strings.TrimPrefix(f.URL, "https")+"/api/connect/"+f.Device["id"], headers)
	if e != nil {
		panic(e)
	}
	defer ws.Close()
	for {
		kind, _ := read()
		if kind == "login" {
			break
		}
	}
	begin("/usr/local/bin/rtty-tenant-poc -S " + remoteDir + "/proof.bin")
	download("download after websocket reconnect")
	begin("rm -f " + remoteDir + "/proof.bin && rmdir " + remoteDir + " && printf 'RTTY_%s\\n' 'CLEANED'")
	for !strings.Contains(terminal, "RTTY_CLEANED") {
		read()
	}
	check("temporary file cleanup", true, remoteDir)
}

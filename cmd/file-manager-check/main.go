// Real HTTP/WebSocket file-manager acceptance and matched-path performance probe.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/gorilla/websocket"
)

type fixture struct {
	URL    string                       `json:"url"`
	Tenant map[string]string            `json:"tenant_a"`
	Device map[string]string            `json:"device"`
	Users  map[string]map[string]string `json:"users"`
}

var records = []map[string]any{}
var evidence string

func record(name string, ok bool, data any) {
	records = append(records, map[string]any{"check": name, "passed": ok, "data": data})
	raw, _ := json.MarshalIndent(records, "", "  ")
	os.WriteFile(evidence, raw, 0600)
	fmt.Println(name, ok, data)
	if !ok {
		panic(name)
	}
}
func main() {
	link := flag.String("link", "", "physical-device link selector: 5g or 14g")
	access := flag.String("access", "", "private access JSON")
	ca := flag.String("ca", "", "TLS CA")
	source := flag.String("source", "", "device download file")
	dir := flag.String("directory", "", "disposable writable target directory")
	out := flag.String("output", "/tmp/file-manager-check.json", "evidence output")
	downloadOnly := flag.Bool("download-only", false, "only measure download")
	readDelay := flag.Duration("read-delay", 0, "simulate slow binary consumption")
	flag.Parse()
	evidence = *out
	var f fixture
	raw, err := os.ReadFile(*access)
	if err != nil {
		panic(err)
	}
	if json.Unmarshal(raw, &f) != nil {
		panic("fixture")
	}
	cert, err := os.ReadFile(*ca)
	if err != nil {
		panic(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		panic("CA")
	}
	tlsConfig := &tls.Config{RootCAs: roots}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	call := func(method, p string, v any) (int, []byte) {
		var body io.Reader
		if v != nil {
			b, _ := json.Marshal(v)
			body = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, f.URL+p, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", f.URL)
		r, e := client.Do(req)
		if e != nil {
			panic(e)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, b
	}
	code, _ := call("POST", "/api/signin", map[string]string{"username": "admin", "password": f.Users["admin"]["password"]})
	record("login", code == 200, code)
	code, _ = call("POST", "/api/tenant", map[string]string{"tenant_id": f.Tenant["id"]})
	record("tenant", code == 200, code)
	digest := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	transfer := func(direction, target string, payload []byte, checksum, mode string) ([]byte, error, float64) {
		query := url.Values{"direction": {direction}, "path": {target}, "link": {*link}}
		if direction == "upload" {
			query.Set("size", fmt.Sprint(len(payload)))
			query.Set("sha256", checksum)
		}
		u, _ := url.Parse(f.URL)
		h := http.Header{"Origin": {f.URL}}
		for _, cookie := range jar.Cookies(u) {
			h.Add("Cookie", cookie.String())
		}
		start := time.Now()
		conn, resp, e := (&websocket.Dialer{TLSClientConfig: tlsConfig, HandshakeTimeout: 15 * time.Second}).Dial("wss://"+u.Host+"/api/devices/"+f.Device["id"]+"/file-transfer?"+query.Encode(), h)
		if e != nil {
			if resp != nil {
				return nil, fmt.Errorf("HTTP %d", resp.StatusCode), time.Since(start).Seconds()
			}
			return nil, e, time.Since(start).Seconds()
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Minute))
		var download []byte
		offset, credits := 0, 0
		id := ""
		cancelled := false
		if mode == "terminal-injection" {
			conn.WriteMessage(websocket.BinaryMessage, []byte{0, 'i', 'd', '\n'})
		}
		pump := func() error {
			for credits > 0 && offset < len(payload) {
				end := min(offset+32768, len(payload))
				frame := append([]byte{1, 3}, payload[offset:end]...)
				if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
					return err
				}
				offset = end
				credits--
				if mode == "cancel" && !cancelled {
					cancelled = true
					return conn.WriteJSON(map[string]string{"type": "cancel"})
				}
			}
			return nil
		}
		for {
			typ, b, e := conn.ReadMessage()
			if e != nil {
				return download, e, time.Since(start).Seconds()
			}
			if typ == websocket.BinaryMessage {
				if *readDelay > 0 {
					time.Sleep(*readDelay)
				}
				if len(b) == 0 || b[0] != 1 {
					return download, fmt.Errorf("non-file data exposed"), time.Since(start).Seconds()
				}
				if len(b) > 1 {
					download = append(download, b[1:]...)
					conn.WriteJSON(map[string]string{"type": "fileAck", "transfer_id": id})
				}
				continue
			}
			var m struct {
				Type   string `json:"type"`
				ID     string `json:"transfer_id"`
				Window int    `json:"window"`
				SHA    string `json:"sha256"`
				Size   int    `json:"size"`
				Error  string `json:"error"`
			}
			if json.Unmarshal(b, &m) != nil {
				return nil, fmt.Errorf("invalid JSON"), 0
			}
			switch m.Type {
			case "error", "fileError":
				return download, fmt.Errorf("%s", m.Error), time.Since(start).Seconds()
			case "sendfile":
				id = m.ID
				conn.WriteJSON(map[string]any{"type": "fileStart", "transfer_id": id, "window": 16})
			case "uploadReady":
				credits = m.Window
				if len(payload) == 0 {
					conn.WriteMessage(websocket.BinaryMessage, []byte{1, 3})
				} else if e = pump(); e != nil {
					return nil, e, 0
				}
			case "fileAck":
				if !cancelled {
					credits++
					if e = pump(); e != nil {
						return nil, e, 0
					}
				}
			case "done":
				actual := payload
				if direction == "download" {
					actual = download
				}
				if len(actual) != m.Size || digest(actual) != m.SHA {
					return download, fmt.Errorf("size or hash mismatch"), time.Since(start).Seconds()
				}
				return download, nil, time.Since(start).Seconds()
			}
		}
	}
	data, err, seconds := transfer("download", *source, nil, "", "")
	record("binary download", err == nil, map[string]any{"error": fmt.Sprint(err), "bytes": len(data), "seconds": seconds, "MiB_per_second": float64(len(data)) / 1048576 / seconds, "sha256": digest(data)})
	if *downloadOnly {
		return
	}
	payload := make([]byte, 1024*1024)
	rand.Read(payload)
	target := path.Join(*dir, fmt.Sprintf("binary-upload-%d.bin", time.Now().UnixNano()))
	_, err, seconds = transfer("upload", target, payload, digest(payload), "")
	record("binary upload", err == nil, map[string]any{"error": fmt.Sprint(err), "bytes": len(payload), "seconds": seconds, "MiB_per_second": float64(len(payload)) / 1048576 / seconds, "sha256": digest(payload), "path": target})
	data, err, seconds = transfer("download", target, nil, "", "")
	record("uploaded file download integrity", err == nil && bytes.Equal(data, payload), map[string]any{"error": fmt.Sprint(err), "seconds": seconds, "MiB_per_second": float64(len(data)) / 1048576 / seconds, "sha256": digest(data)})
	_, err, _ = transfer("upload", target, payload, digest(payload), "")
	record("existing file never overwritten", err != nil, fmt.Sprint(err))
	bad := target + ".bad"
	_, err, _ = transfer("upload", bad, payload, fmt.Sprintf("%064d", 0), "")
	record("bad SHA rejected", err != nil, fmt.Sprint(err))
	cancel := target + ".cancel"
	_, err, _ = transfer("upload", cancel, payload, digest(payload), "cancel")
	record("cancel rejected as success", err != nil, fmt.Sprint(err))
	_, err, _ = transfer("download", *source, nil, "", "terminal-injection")
	record("terminal injection rejected", err != nil, fmt.Sprint(err))
	time.Sleep(300 * time.Millisecond)
	code, b := call("POST", "/api/devices/"+f.Device["id"]+"/files?link="+url.QueryEscape(*link), map[string]string{"action": "list", "path": *dir})
	record("directory usable after cancellation", code == 200, code)
	var listing struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	json.Unmarshal(b, &listing)
	absent := true
	for _, entry := range listing.Entries {
		if entry.Name == path.Base(bad) || entry.Name == path.Base(cancel) {
			absent = false
		}
	}
	record("failed uploads unpublished", absent, absent)
}

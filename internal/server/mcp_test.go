package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-oauth2/oauth2/v4/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zhaojh329/rtty-go/proto"
)

type mcpFixture struct {
	t *testing.T
	m *terminalMCP
	r *gin.Engine
}
type testTokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Scope   string `json:"scope"`
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
PRAGMA foreign_keys=ON;
CREATE TABLE users(id TEXT PRIMARY KEY,username TEXT,password BLOB,admin INTEGER,disabled INTEGER DEFAULT 0);
CREATE TABLE tenants(id TEXT PRIMARY KEY,name TEXT,disabled INTEGER DEFAULT 0);
CREATE TABLE members(tenant TEXT,user TEXT,role TEXT,PRIMARY KEY(tenant,user));
CREATE TABLE devices(id TEXT PRIMARY KEY,tenant TEXT,wire_id TEXT,grp TEXT DEFAULT '',name TEXT,key_hash TEXT,disabled INTEGER DEFAULT 0,web_targets TEXT DEFAULT '[]');
CREATE TABLE grants(tenant TEXT,user TEXT,device TEXT DEFAULT '',grp TEXT DEFAULT '');
CREATE TABLE sessions(hash TEXT PRIMARY KEY,user TEXT,tenant TEXT,expires INTEGER);
CREATE TABLE audit(id INTEGER PRIMARY KEY,at INTEGER,user TEXT,tenant TEXT,action TEXT,resource TEXT,result TEXT);
INSERT INTO users VALUES('admin','admin',X'00',1,0),('op','operator',X'00',0,0),('viewer','viewer',X'00',0,0);
INSERT INTO tenants VALUES('a','Tenant A',0),('b','Tenant B',0);
INSERT INTO members VALUES('a','op','operator'),('a','viewer','viewer');
INSERT INTO devices(id,tenant,wire_id,name) VALUES('d1','a','device-one','One'),('d2','b','device-two','Two'),('d3','a','device-three','Three');
INSERT INTO grants VALUES('a','op','d1','');
`)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateDeviceLifecycle(db); err != nil {
		t.Fatal(err)
	}
	if err = migrateTerminalMCP(db); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{MCPURL: "https://console.test/mcp", MCPDiagnosticUser: "diagnostic"})
	a := &TenantAuth{db: db, srv: srv, live: map[string]*accessLease{}, attempts: map[string]loginAttempt{}, passwordSlots: make(chan struct{}, 4)}
	srv.tenant = a
	m, err := newTerminalMCP(a)
	if err != nil {
		t.Fatal(err)
	}
	srv.mcp = m
	for _, user := range []string{"admin", "op", "viewer"} {
		if _, err = db.Exec("INSERT INTO sessions VALUES(?,?,?,?)", digest(user+"-browser"), user, "a", time.Now().Add(time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"admin", "op"} {
		scope, _ := normalizeScopes(strings.Join(mcpScopes, " "))
		if _, err = db.Exec("INSERT INTO mcp_policies VALUES('a',?,?)", user, scope); err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	a.routes(r)
	m.routes(r)
	return &mcpFixture{t, m, r}
}

func (f *mcpFixture) request(method, path, cookie, contentType string, body io.Reader) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, "https://console.test"+path, body)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: tenantSessionCookie, Value: cookie + "-browser"})
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}
func (f *mcpFixture) json(method, path, cookie string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return f.request(method, path, cookie, "application/json", bytes.NewReader(b))
}
func (f *mcpFixture) begin(scope string) (string, string) {
	f.t.Helper()
	verifier := strings.Repeat("v", 64)
	h := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {f.m.clientID}, "redirect_uri": {"http://127.0.0.1:54321/callback"}, "state": {strings.Repeat("s", 32)}, "resource": {f.m.resource}, "scope": {scope}, "code_challenge": {base64.RawURLEncoding.EncodeToString(h[:])}, "code_challenge_method": {"S256"}}
	w := f.request("GET", "/oauth/authorize?"+q.Encode(), "", "", nil)
	if w.Code != 302 {
		f.t.Fatalf("authorize: %d %s", w.Code, w.Body.String())
	}
	u, _ := url.Parse(w.Header().Get("Location"))
	return u.Query().Get("request"), verifier
}
func (f *mcpFixture) code(user, scope string) (string, string) {
	f.t.Helper()
	nonce, verifier := f.begin(scope)
	w := f.request("GET", "/api/oauth/requests/"+nonce, user, "", nil)
	if w.Code != 200 {
		f.t.Fatalf("consent request: %d %s", w.Code, w.Body.String())
	}
	w = f.json("POST", "/api/oauth/authorize", user, map[string]any{"request": nonce, "tenant_id": "a", "scopes": strings.Fields(scope), "approve": true})
	if w.Code != 200 {
		f.t.Fatalf("consent: %d %s", w.Code, w.Body.String())
	}
	var response struct{ Redirect string }
	json.Unmarshal(w.Body.Bytes(), &response)
	u, _ := url.Parse(response.Redirect)
	if u.Query().Get("iss") != f.m.issuer || u.Query().Get("state") != strings.Repeat("s", 32) {
		f.t.Fatal("authorization response missing issuer or state")
	}
	return u.Query().Get("code"), verifier
}
func (f *mcpFixture) exchange(code, verifier string) *httptest.ResponseRecorder {
	return f.form(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:54321/callback"}, "client_id": {f.m.clientID}, "resource": {f.m.resource}})
}
func (f *mcpFixture) form(v url.Values) *httptest.ResponseRecorder {
	return f.request("POST", "/oauth/token", "", "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
}
func (f *mcpFixture) login(user, scope string) testTokens {
	f.t.Helper()
	code, verifier := f.code(user, scope)
	w := f.exchange(code, verifier)
	if w.Code != 200 {
		f.t.Fatalf("exchange: %d %s", w.Code, w.Body.String())
	}
	var result testTokens
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Access == "" || result.Refresh == "" {
		f.t.Fatalf("invalid token response: %s", w.Body.String())
	}
	return result
}
func (f *mcpFixture) refresh(refresh, scope, resource string) *httptest.ResponseRecorder {
	return f.form(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {f.m.clientID}, "resource": {resource}, "scope": {scope}})
}

func TestMCPOAuthFlowAndRefreshReplay(t *testing.T) {
	f := newMCPFixture(t)
	code, verifier := f.code("op", scopeDevices)
	if w := f.exchange(code, strings.Repeat("x", 64)); w.Code != 400 {
		t.Fatal("wrong PKCE accepted")
	}
	// A failed PKCE attempt consumes the authorization code as well.
	if w := f.exchange(code, verifier); w.Code != 400 {
		t.Fatal("failed authorization code remained usable")
	}
	code, verifier = f.code("op", scopeDevices)
	w := f.exchange(code, verifier)
	if w.Code != 200 {
		t.Fatalf("valid exchange: %s", w.Body.String())
	}
	var first testTokens
	json.Unmarshal(w.Body.Bytes(), &first)
	if w = f.exchange(code, verifier); w.Code != 400 {
		t.Fatal("code reused")
	}
	if _, err := f.m.verifyToken(context.Background(), first.Access, nil); err != nil {
		t.Fatal(err)
	}
	if w = f.refresh(first.Refresh, "", "https://elsewhere.test/mcp"); w.Code != 400 {
		t.Fatal("wrong resource accepted")
	}
	if w = f.refresh(first.Refresh, scopeDevices+" "+scopeTerminal, f.m.resource); w.Code != 400 {
		t.Fatal("scope escalation accepted")
	}
	w = f.refresh(first.Refresh, "", f.m.resource)
	if w.Code != 200 {
		t.Fatalf("refresh: %s", w.Body.String())
	}
	var second testTokens
	json.Unmarshal(w.Body.Bytes(), &second)
	if second.Access == first.Access || second.Refresh == first.Refresh {
		t.Fatal("credentials did not rotate")
	}
	if _, err := f.m.verifyToken(context.Background(), second.Access, nil); err != nil {
		t.Fatal(err)
	}
	if w = f.refresh(first.Refresh, "", f.m.resource); w.Code != 400 {
		t.Fatal("rotated refresh reused")
	}
	if _, err := f.m.verifyToken(context.Background(), second.Access, nil); err == nil {
		t.Fatal("refresh replay did not revoke the grant")
	}
	// No access, refresh or authorization code plaintext may be persisted.
	rows, err := f.m.a.db.Query("SELECT coalesce(code_hash,''),coalesce(access_hash,''),coalesce(refresh_hash,''),body FROM oauth_tokens")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c, a, r, b string
		rows.Scan(&c, &a, &r, &b)
		for _, secret := range []string{code, first.Access, first.Refresh, second.Access, second.Refresh} {
			if strings.Contains(c+a+r+b, secret) {
				t.Fatal("plaintext OAuth credential persisted")
			}
		}
	}
}

func TestMCPOAuthAdmissionAndPolicy(t *testing.T) {
	f := newMCPFixture(t)
	nonce, _ := f.begin(scopeDevices + " " + scopeTerminal)
	if w := f.json("POST", "/api/oauth/authorize", "op", map[string]any{"request": nonce, "tenant_id": "a", "scopes": []string{scopeDevices}, "approve": true}); w.Code != 404 {
		t.Fatal("unbound consent accepted")
	}
	if w := f.request("GET", "/api/oauth/requests/"+nonce, "op", "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "/api/oauth/requests/"+nonce, "admin", "", nil); w.Code != 404 {
		t.Fatal("another browser took over the request")
	}
	for _, tenant := range []string{"", "b"} {
		w := f.json("POST", "/api/oauth/authorize", "op", map[string]any{"request": nonce, "tenant_id": tenant, "scopes": []string{scopeDevices}, "approve": true})
		if w.Code != 403 {
			t.Fatalf("foreign tenant consent: %d", w.Code)
		}
	}
	if w := f.json("PUT", "/api/manage/mcp-policies", "op", map[string]any{"user_id": "op", "scopes": []string{scopeTerminal}}); w.Code != 403 {
		t.Fatal("operator edited MCP policy")
	}
	if w := f.json("PUT", "/api/manage/mcp-policies", "admin", map[string]any{"user_id": "viewer", "scopes": []string{scopeTerminal}}); w.Code != 400 {
		t.Fatal("viewer got terminal permission")
	}
	if w := f.json("PUT", "/api/manage/mcp-policies", "admin", map[string]any{"user_id": "op", "scopes": []string{}}); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := f.json("POST", "/api/oauth/authorize", "op", map[string]any{"request": nonce, "tenant_id": "a", "scopes": []string{scopeDevices}, "approve": true}); w.Code != 403 {
		t.Fatal("disabled MCP policy bypassed")
	}
	if w := f.request("GET", "/api/oauth/grants", "", "", nil); w.Code != 401 {
		t.Fatal("anonymous grant listing")
	}
	for _, candidate := range []string{"https://evil.test/callback", "http://localhost:5000/callback", "http://127.0.0.1:5000/other", "http://127.0.0.1:5000/callback?x=1"} {
		if registeredRedirect(f.m.redirect, candidate) {
			t.Fatalf("callback accepted: %s", candidate)
		}
	}
	for _, candidate := range []string{"http://127.0.0.1/callback", "http://127.0.0.1:5000/callback"} {
		if !registeredRedirect(f.m.redirect, candidate) {
			t.Fatal(candidate)
		}
	}
}

type mcpRoundTripper func(*http.Request) (*http.Response, error)

func (f mcpRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func (f *mcpFixture) client(token string) *mcp.ClientSession {
	f.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	httpClient := &http.Client{Transport: mcpRoundTripper(func(req *http.Request) (*http.Response, error) {
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		f.r.ServeHTTP(w, req)
		r := w.Result()
		r.Request = req
		return r, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	f.t.Cleanup(cancel)
	s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.m.resource, HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { s.Close() })
	return s
}
func callMCP(t *testing.T, s *mcp.ClientSession, name string, args any, ok bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if !ok {
		if err == nil && !r.IsError {
			t.Fatalf("%s unexpectedly succeeded", name)
		}
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Fatalf("%s: %v", name, r.Content)
	}
	b, _ := json.Marshal(r.StructuredContent)
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		t.Fatalf("invalid tool response %s", b)
	}
	return out
}

func TestMCPTransportAuthenticationAndDeviceIsolation(t *testing.T) {
	f := newMCPFixture(t)
	for _, path := range []string{"/mcp", "/mcp?access_token=bad"} {
		w := f.request("POST", path, "op", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if path == "/mcp" && (w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), "resource_metadata=")) {
			t.Fatalf("cookie authenticated MCP: %d", w.Code)
		}
		if path != "/mcp" && w.Code != 400 {
			t.Fatal("query token accepted")
		}
	}
	token := f.login("op", scopeDevices)
	s := f.client(token.Access)
	out := callMCP(t, s, "list_devices", map[string]any{}, true)
	devices := out["devices"].([]any)
	if len(devices) != 1 || devices[0].(map[string]any)["id"] != "d1" {
		t.Fatalf("device scope leak: %v", out)
	}
	callMCP(t, s, "open_terminal", map[string]any{"device_id": "d1"}, false)
	f.m.a.mu.Lock()
	_, err := f.m.a.db.Exec("DELETE FROM sessions WHERE user='op'")
	f.m.a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	callMCP(t, s, "list_devices", map[string]any{}, false)
}

func (f *mcpFixture) device(uid string) (*Device, <-chan string) {
	f.t.Helper()
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	dev := &Device{id: "d1", group: "a", proto: 6, ctx: ctx, cancel: cancel, conn: server, msg: proto.NewMsgReaderWriter(proto.RoleRttys, server)}
	if !f.m.a.srv.AddDevice(dev) {
		f.t.Fatal("device registration")
	}
	input := make(chan string, 8)
	reader := proto.NewMsgReaderWriter(proto.RoleRtty, client)
	f.t.Cleanup(func() { cancel(); server.Close(); client.Close() })
	go func() {
		for {
			typ, data, err := reader.Read()
			if err != nil {
				return
			}
			switch typ {
			case proto.MsgTypeLogin:
				handleLoginMsg(dev, append(append([]byte{}, data...), 0))
			case proto.MsgTypeTermData:
				copyData := append([]byte{}, data...)
				input <- string(copyData[32:])
				go handleTermDataMsg(dev, copyData)
			case proto.MsgTypeCmd:
				parts := bytes.SplitN(data, []byte{0}, 4)
				if len(parts) != 4 {
					return
				}
				output := "diagnostic output\n"
				if string(parts[1]) == "id" && bytes.Contains(parts[3], []byte("-u")) {
					output = uid + "\n"
				}
				response, _ := json.Marshal(CommandRespInfo{Token: string(parts[2]), Attrs: json.RawMessage(`{"code":0,"stdout":"` + base64.StdEncoding.EncodeToString([]byte(output)) + `","stderr":""}`)})
				handleCmdMsg(dev, response)
			}
		}
	}()
	return dev, input
}

func TestMCPTerminalLifecycleOwnershipAndRevocation(t *testing.T) {
	f := newMCPFixture(t)
	_, input := f.device("1001")
	first := f.login("op", scopeDevices+" "+scopeTerminal)
	s := f.client(first.Access)
	callMCP(t, s, "open_terminal", map[string]any{"device_id": "d2"}, false)
	callMCP(t, s, "open_terminal", map[string]any{"device_id": "d3"}, false)
	open := callMCP(t, s, "open_terminal", map[string]any{"device_id": "d1"}, true)
	id := open["session_id"].(string)
	second := f.login("op", scopeTerminal)
	other := f.client(second.Access)
	callMCP(t, other, "write_terminal", map[string]any{"session_id": id, "input": "wrong\n"}, false)
	callMCP(t, s, "write_terminal", map[string]any{"session_id": id, "input": "echo hello\n"}, true)
	select {
	case got := <-input:
		if got != "echo hello\n" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("input not forwarded")
	}
	var output string
	for i := 0; i < 4 && output == ""; i++ {
		out := callMCP(t, s, "read_terminal", map[string]any{"session_id": id, "wait_ms": 1000}, true)
		output = out["output"].(string)
	}
	if output != "echo hello\n" {
		t.Fatalf("terminal output: %q", output)
	}
	w := f.json("PUT", "/api/manage/mcp-policies", "admin", map[string]any{"user_id": "op", "scopes": []string{scopeDevices}})
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	callMCP(t, s, "write_terminal", map[string]any{"session_id": id, "input": "after-revoke\n"}, false)
	f.m.termMu.Lock()
	p := f.m.terminals[id]
	f.m.termMu.Unlock()
	if p != nil {
		select {
		case <-p.done:
		case <-time.After(time.Second):
			t.Fatal("revoked terminal not closed")
		}
	}
	select {
	case <-input:
		t.Fatal("revoked input reached device")
	default:
	}
}

func TestMCPDiagnosticTemplatesAndNonRoot(t *testing.T) {
	for _, in := range []diagnosticInput{{Check: "sh"}, {Check: "logs", Unit: "-x.service"}, {Check: "logs", Unit: "a.service;id"}, {Check: "disk", Unit: "a.service"}} {
		if _, _, err := diagnosticCommand(in); err == nil {
			t.Fatalf("unsafe diagnostic: %+v", in)
		}
	}
	for _, uid := range []string{"1001", "0"} {
		t.Run(uid, func(t *testing.T) {
			f := newMCPFixture(t)
			f.device(uid)
			token := f.login("op", scopeDiagnostic)
			s := f.client(token.Access)
			out := callMCP(t, s, "run_diagnostic", map[string]any{"device_id": "d1", "check": "disk"}, uid != "0")
			if uid != "0" && out["stdout"] != "diagnostic output\n" {
				t.Fatal(out)
			}
		})
	}
}

func TestMCPOAuthConcurrentCodeUse(t *testing.T) {
	f := newMCPFixture(t)
	code, verifier := f.code("op", scopeDevices)
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- f.exchange(code, verifier).Code }()
	}
	wg.Wait()
	close(results)
	success := 0
	for status := range results {
		if status == 200 {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("one-use code exchanged %d times", success)
	}
}

func TestMCPRevocationAPIsAndLiveDeviceGrant(t *testing.T) {
	t.Run("own grant", func(t *testing.T) {
		f := newMCPFixture(t)
		token := f.login("op", scopeDevices)
		ti, err := f.m.verifyToken(context.Background(), token.Access, nil)
		if err != nil {
			t.Fatal(err)
		}
		if w := f.request("DELETE", "/api/oauth/grants/"+ti.UserID, "admin", "", nil); w.Code != 404 {
			t.Fatal("another user revoked this grant")
		}
		if w := f.request("DELETE", "/api/oauth/grants/"+ti.UserID, "op", "", nil); w.Code != 204 {
			t.Fatal(w.Code)
		}
		if _, err = f.m.verifyToken(context.Background(), token.Access, nil); err == nil {
			t.Fatal("revoked token valid")
		}
	})
	t.Run("device grant", func(t *testing.T) {
		f := newMCPFixture(t)
		f.device("1001")
		token := f.login("op", scopeTerminal)
		s := f.client(token.Access)
		out := callMCP(t, s, "open_terminal", map[string]any{"device_id": "d1"}, true)
		w := f.json("PUT", "/api/manage/grants", "admin", map[string]any{"user_id": "op", "device_id": "d1", "group": "", "access": false})
		if w.Code != 200 {
			t.Fatalf("remove device grant: %d %s", w.Code, w.Body.String())
		}
		callMCP(t, s, "write_terminal", map[string]any{"session_id": out["session_id"], "input": "denied\n"}, false)
	})
	for _, mutation := range []string{"UPDATE users SET disabled=1 WHERE id='op'", "DELETE FROM members WHERE user='op'", "UPDATE tenants SET disabled=1 WHERE id='a'", "DELETE FROM sessions WHERE user='op'"} {
		t.Run(mutation, func(t *testing.T) {
			f := newMCPFixture(t)
			token := f.login("op", scopeDevices)
			f.m.a.mu.Lock()
			_, err := f.m.a.db.Exec(mutation)
			f.m.a.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.m.verifyToken(context.Background(), token.Access, nil); err == nil {
				t.Fatal("account/tenant/session revocation bypassed")
			}
		})
	}
}

func TestMCPCredentialExpiryPersistenceAndResourceBinding(t *testing.T) {
	f := newMCPFixture(t)
	token := f.login("op", scopeDevices)
	restarted, err := newTerminalMCP(f.m.a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.verifyToken(context.Background(), token.Access, nil); err != nil {
		t.Fatal("persisted token not available after restart")
	}
	info, err := f.m.store.GetByAccess(context.Background(), token.Access)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(info)
	stored := models.NewToken()
	json.Unmarshal(encoded, stored)
	stored.Access = ""
	stored.Extension.Set("resource", "https://other.test/mcp")
	encoded, _ = json.Marshal(stored)
	if _, err = f.m.a.db.Exec("UPDATE oauth_tokens SET body=? WHERE access_hash=?", string(encoded), digest(token.Access)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.verifyToken(context.Background(), token.Access, nil); err == nil {
		t.Fatal("wrong token resource accepted")
	}
	stored.Extension.Set("resource", f.m.resource)
	stored.AccessCreateAt = time.Now().Add(-time.Hour)
	encoded, _ = json.Marshal(stored)
	f.m.a.db.Exec("UPDATE oauth_tokens SET body=? WHERE access_hash=?", string(encoded), digest(token.Access))
	if _, err = f.m.verifyToken(context.Background(), token.Access, nil); err == nil {
		t.Fatal("expired access token accepted")
	}
}

func TestMCPPolicyFailureRollsBack(t *testing.T) {
	f := newMCPFixture(t)
	token := f.login("op", scopeDevices)
	_, err := f.m.a.db.Exec("CREATE TRIGGER reject_policy_audit BEFORE INSERT ON audit WHEN NEW.action='mcp-policy' BEGIN SELECT RAISE(FAIL,'audit unavailable'); END;")
	if err != nil {
		t.Fatal(err)
	}
	w := f.json("PUT", "/api/manage/mcp-policies", "admin", map[string]any{"user_id": "op", "scopes": []string{}})
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	if _, err = f.m.verifyToken(context.Background(), token.Access, nil); err != nil {
		t.Fatal("failed policy edit partially revoked the grant")
	}
}

func TestMCPRejectsUntrustedConfiguration(t *testing.T) {
	f := newMCPFixture(t)
	for _, resource := range []string{"http://console.test/mcp", "https://user:pw@console.test/mcp", "https://console.test/other", "https://console.test/mcp?token=x", "https://console.test/mcp#x", "https://console.test/%6dcp", "https://console.test/mcp?"} {
		f.m.a.srv.cfg.MCPURL = resource
		if _, err := newTerminalMCP(f.m.a); err == nil {
			t.Fatal(resource)
		}
	}
	f.m.a.srv.cfg.MCPURL = f.m.resource
	f.m.a.srv.cfg.MCPDiagnosticUser = "root"
	if _, err := newTerminalMCP(f.m.a); err == nil {
		t.Fatal("root diagnostic account accepted")
	}
	f.m.a.srv.cfg.MCPDiagnosticUser = "diagnostic"
	f.m.a.srv.cfg.MCPRedirectURI = "http://evil.test/callback"
	if _, err := newTerminalMCP(f.m.a); err == nil {
		t.Fatal("remote plaintext callback accepted")
	}
}

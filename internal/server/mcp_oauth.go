package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-oauth2/oauth2/v4"
	"github.com/go-oauth2/oauth2/v4/manage"
	"github.com/go-oauth2/oauth2/v4/models"
	oauthserver "github.com/go-oauth2/oauth2/v4/server"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	scopeDevices        = "devices:read"
	scopeDiagnostic     = "diagnostics:read"
	scopeTerminal       = "terminal:interactive"
	oauthAccessLifetime = 10 * time.Minute
	oauthGrantLifetime  = 24 * time.Hour
)

var mcpScopes = []string{scopeDevices, scopeDiagnostic, scopeTerminal}
var osAccountName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

type terminalMCP struct {
	a                                          *TenantAuth
	issuer, resource, host, clientID, redirect string
	manager                                    *manage.Manager
	oauth                                      *oauthserver.Server
	store                                      *oauthSQLStore
	exchangeMu                                 sync.Mutex // serialize one-use code exchange and refresh rotation
	termMu                                     sync.Mutex
	terminals                                  map[string]*mcpTerminal
	diagnosticSlots                            chan struct{}
	handler                                    http.Handler
}

type mcpClientStore struct{ m *terminalMCP }

func (s mcpClientStore) GetByID(_ context.Context, id string) (oauth2.ClientInfo, error) {
	if id != s.m.clientID {
		return nil, errDenied
	}
	return &models.Client{ID: id, Domain: s.m.redirect, Public: true}, nil
}

type oauthCodeGenerator struct{}

func (oauthCodeGenerator) Token(context.Context, *oauth2.GenerateBasic) (string, error) {
	return randomID(), nil
}

type oauthAccessGenerator struct{}

func (oauthAccessGenerator) Token(_ context.Context, _ *oauth2.GenerateBasic, refresh bool) (string, string, error) {
	r := ""
	if refresh {
		r = randomID()
	}
	return randomID(), r, nil
}

func newTerminalMCP(a *TenantAuth) (*terminalMCP, error) {
	cfg := a.srv.cfg
	u, err := url.Parse(cfg.MCPURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "/mcp" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("mcp-url must be a canonical HTTPS URL ending in /mcp")
	}
	m := &terminalMCP{a: a, resource: u.String(), issuer: "https://" + u.Host, host: u.Host, clientID: cfg.MCPClientID, redirect: cfg.MCPRedirectURI, terminals: map[string]*mcpTerminal{}, diagnosticSlots: make(chan struct{}, 8)}
	if m.clientID == "" {
		m.clientID = "ja-rtty-codex"
	}
	if m.redirect == "" {
		m.redirect = "http://127.0.0.1/callback"
	}
	if !validName(m.clientID) || strings.ContainsAny(m.clientID, " \t") {
		return nil, fmt.Errorf("invalid mcp-client-id")
	}
	redirect, err := url.Parse(m.redirect)
	if err != nil || redirect.Hostname() == "" || redirect.User != nil || redirect.RawQuery != "" || redirect.ForceQuery || redirect.Fragment != "" || (redirect.Scheme != "https" && !(redirect.Scheme == "http" && loopbackURL(redirect))) {
		return nil, fmt.Errorf("mcp-redirect-uri requires HTTPS or an explicit loopback IP callback")
	}
	if cfg.MCPDiagnosticUser != "" && (!osAccountName.MatchString(cfg.MCPDiagnosticUser) || cfg.MCPDiagnosticUser == "root") {
		return nil, fmt.Errorf("mcp-diagnostic-user must name a non-root OS account")
	}
	m.store = &oauthSQLStore{m: m}
	m.manager = manage.NewDefaultManager()
	m.manager.MapClientStorage(mcpClientStore{m})
	m.manager.MapTokenStorage(m.store)
	m.manager.MapAuthorizeGenerate(oauthCodeGenerator{})
	m.manager.MapAccessGenerate(oauthAccessGenerator{})
	m.manager.SetAuthorizeCodeExp(90 * time.Second)
	m.manager.SetAuthorizeCodeTokenCfg(&manage.Config{AccessTokenExp: oauthAccessLifetime, RefreshTokenExp: oauthGrantLifetime, IsGenerateRefresh: true})
	m.manager.SetRefreshTokenCfg(&manage.RefreshingConfig{AccessTokenExp: oauthAccessLifetime, IsGenerateRefresh: true, IsRemoveRefreshing: true})
	m.manager.SetValidateURIHandler(func(base, candidate string) error {
		if !registeredRedirect(base, candidate) {
			return errDenied
		}
		return nil
	})
	m.manager.SetExtractExtensionHandler(func(_ *oauth2.TokenGenerateRequest, ti oauth2.ExtendableTokenInfo) {
		ti.SetExtension(url.Values{"resource": {m.resource}})
	})
	c := oauthserver.NewConfig()
	c.AllowedResponseTypes = []oauth2.ResponseType{oauth2.Code}
	c.AllowedGrantTypes = []oauth2.GrantType{oauth2.AuthorizationCode, oauth2.Refreshing}
	c.AllowedCodeChallengeMethods = []oauth2.CodeChallengeMethod{oauth2.CodeChallengeS256}
	c.ForcePKCE = true
	m.oauth = oauthserver.NewServer(c, m.manager)
	m.oauth.SetClientInfoHandler(oauthserver.ClientFormHandler)
	m.oauth.SetRefreshingScopeHandler(func(tgr *oauth2.TokenGenerateRequest, old string) (bool, error) { return tgr.Scope == old, nil })
	m.handler = m.newHandler()
	return m, nil
}

func loopbackURL(u *url.URL) bool {
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
func registeredRedirect(base, candidate string) bool {
	if base == candidate {
		return true
	}
	b, e1 := url.Parse(base)
	c, e2 := url.Parse(candidate)
	if e1 != nil || e2 != nil || b.Scheme != "http" || !loopbackURL(b) || b.Port() != "" || c.User != nil || c.RawQuery != "" || c.ForceQuery || c.Fragment != "" {
		return false
	}
	// RFC 8252: only the ephemeral port may vary for a registered loopback IP.
	return c.Scheme == b.Scheme && c.Hostname() == b.Hostname() && c.Path == b.Path && c.RawPath == b.RawPath
}
func hasScope(value, scope string) bool { return slices.Contains(strings.Fields(value), scope) }
func scopeSubset(subset, all string) bool {
	for _, s := range strings.Fields(subset) {
		if !hasScope(all, s) {
			return false
		}
	}
	return true
}
func normalizeScopes(value string) (string, error) {
	values := strings.Fields(value)
	if len(values) == 0 || len(values) > len(mcpScopes) {
		return "", errDenied
	}
	out := []string{}
	for _, scope := range values {
		if !slices.Contains(mcpScopes, scope) || slices.Contains(out, scope) {
			return "", errDenied
		}
		out = append(out, scope)
	}
	slices.Sort(out)
	return strings.Join(out, " "), nil
}

func (m *terminalMCP) routes(r *gin.Engine) {
	metadata := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"resource": m.resource, "authorization_servers": []string{m.issuer}, "scopes_supported": mcpScopes, "bearer_methods_supported": []string{"header"}})
	}
	r.GET("/.well-known/oauth-protected-resource", metadata)
	r.GET("/.well-known/oauth-protected-resource/mcp", metadata)
	r.GET("/.well-known/oauth-authorization-server", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"issuer": m.issuer, "authorization_endpoint": m.issuer + "/oauth/authorize", "token_endpoint": m.issuer + "/oauth/token", "revocation_endpoint": m.issuer + "/oauth/revoke",
			"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"},
			"code_challenge_methods_supported": []string{"S256"}, "scopes_supported": mcpScopes, "authorization_response_iss_parameter_supported": true,
		})
	})
	r.GET("/oauth/authorize", m.authorize)
	r.POST("/oauth/token", m.token)
	r.POST("/oauth/revoke", m.revokeToken)
	r.Any("/mcp", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if c.Request.Host != m.host || (c.GetHeader("Origin") != "" && c.GetHeader("Origin") != m.issuer) {
			c.Status(403)
			return
		}
		// Tokens in query strings and cookies are never accepted, including when
		// a valid Authorization header is also present.
		if c.Request.URL.RawQuery != "" {
			c.Status(400)
			return
		}
		m.handler.ServeHTTP(c.Writer, c.Request)
	})
	api := r.Group("/api", m.a.middleware)
	api.GET("/oauth/requests/:id", m.consentRequest)
	api.POST("/oauth/authorize", m.consent)
	api.GET("/oauth/grants", m.listGrants)
	api.DELETE("/oauth/grants/:id", m.deleteGrant)
	api.GET("/manage/mcp-policies", m.listPolicies)
	api.PUT("/manage/mcp-policies", m.setPolicy)
}

type oauthPending struct {
	ClientID  string `json:"client_id"`
	Redirect  string `json:"redirect_uri"`
	State     string `json:"state"`
	Challenge string `json:"code_challenge"`
	Scope     string `json:"scope"`
}

func (m *terminalMCP) authorize(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	q := c.Request.URL.Query()
	for _, v := range q {
		if len(v) != 1 {
			c.Status(400)
			return
		}
	}
	challenge, err := base64.RawURLEncoding.DecodeString(q.Get("code_challenge"))
	scopes, scopeErr := normalizeScopes(q.Get("scope"))
	if q.Get("scope") == "" {
		scopes = scopeDevices + " " + scopeDiagnostic
		scopes, _ = normalizeScopes(scopes)
		scopeErr = nil
	}
	if c.Request.Host != m.host || len(c.Request.URL.RawQuery) > 8192 || q.Get("client_id") != m.clientID || !registeredRedirect(m.redirect, q.Get("redirect_uri")) || q.Get("response_type") != "code" || q.Get("resource") != m.resource || len(q.Get("state")) < 16 || len(q.Get("state")) > 1024 || q.Get("code_challenge_method") != "S256" || err != nil || len(challenge) != 32 || scopeErr != nil {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	pending := oauthPending{m.clientID, q.Get("redirect_uri"), q.Get("state"), q.Get("code_challenge"), scopes}
	b, _ := json.Marshal(pending)
	nonce := randomID()
	m.a.mu.Lock()
	m.pruneLocked()
	var n int
	err = m.a.db.QueryRow("SELECT count(*) FROM oauth_requests").Scan(&n)
	if err == nil && n < 2048 {
		_, err = m.a.db.Exec("INSERT INTO oauth_requests(hash,payload,expires) VALUES(?,?,?)", digest(nonce), string(b), time.Now().Add(5*time.Minute).Unix())
	} else {
		err = errDenied
	}
	m.a.mu.Unlock()
	if err != nil {
		c.Status(429)
		return
	}
	c.Redirect(302, "/oauth/consent?request="+nonce)
}

func (m *terminalMCP) pendingLocked(nonce, session string, bind bool) (*oauthPending, error) {
	if len(nonce) != 64 {
		return nil, errDenied
	}
	var payload, owner string
	if m.a.db.QueryRow("SELECT payload,session FROM oauth_requests WHERE hash=? AND expires>?", digest(nonce), time.Now().Unix()).Scan(&payload, &owner) != nil || (owner != "" && owner != session) {
		return nil, errDenied
	}
	if bind && owner == "" {
		if _, err := m.a.db.Exec("UPDATE oauth_requests SET session=? WHERE hash=?", session, digest(nonce)); err != nil {
			return nil, err
		}
	}
	if !bind && owner != session {
		return nil, errDenied
	}
	p := new(oauthPending)
	if json.Unmarshal([]byte(payload), p) != nil {
		return nil, errDenied
	}
	return p, nil
}
func (m *terminalMCP) consentRequest(c *gin.Context) {
	m.a.mu.Lock()
	defer m.a.mu.Unlock()
	p, err := m.a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	pending, err := m.pendingLocked(c.Param("id"), p.Session, true)
	if err != nil {
		c.Status(404)
		return
	}
	rows, err := m.a.db.Query("SELECT tenant,scopes FROM mcp_policies WHERE user=?", p.ID)
	if err != nil {
		c.Status(503)
		return
	}
	policies := map[string][]string{}
	for rows.Next() {
		var tenant, scope string
		if rows.Scan(&tenant, &scope) != nil {
			rows.Close()
			c.Status(503)
			return
		}
		policies[tenant] = strings.Fields(scope)
	}
	rows.Close()
	c.JSON(200, gin.H{"client_id": pending.ClientID, "scopes": strings.Fields(pending.Scope), "policies": policies, "grant_lifetime_seconds": int(oauthGrantLifetime.Seconds())})
}

func (m *terminalMCP) actorInTenantLocked(p *Actor, tenant string) (*Actor, error) {
	copy := *p
	copy.Tenant = tenant
	var disabled bool
	if tenant == "" || m.a.db.QueryRow("SELECT disabled FROM tenants WHERE id=?", tenant).Scan(&disabled) != nil || disabled {
		return nil, errDenied
	}
	if copy.Admin {
		copy.Role = "admin"
	} else if m.a.db.QueryRow("SELECT role FROM members WHERE user=? AND tenant=?", p.ID, tenant).Scan(&copy.Role) != nil {
		return nil, errDenied
	}
	return &copy, nil
}

func (m *terminalMCP) consent(c *gin.Context) {
	var in struct {
		Request string   `json:"request"`
		Tenant  string   `json:"tenant_id"`
		Scopes  []string `json:"scopes"`
		Approve bool     `json:"approve"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	m.a.mu.Lock()
	p, err := m.a.actorLocked(tenantSession(c))
	if err != nil {
		m.a.mu.Unlock()
		c.Status(401)
		return
	}
	request, err := m.pendingLocked(in.Request, p.Session, false)
	if err != nil {
		m.a.mu.Unlock()
		c.Status(404)
		return
	}
	redirect, _ := url.Parse(request.Redirect)
	values := redirect.Query()
	values.Set("state", request.State)
	values.Set("iss", m.issuer)
	if !in.Approve {
		m.a.db.Exec("DELETE FROM oauth_requests WHERE hash=?", digest(in.Request))
		m.a.mu.Unlock()
		values.Set("error", "access_denied")
		redirect.RawQuery = values.Encode()
		c.JSON(200, gin.H{"redirect": redirect.String()})
		return
	}
	scope, err := normalizeScopes(strings.Join(in.Scopes, " "))
	actor, actorErr := m.actorInTenantLocked(p, in.Tenant)
	var allowed string
	policyErr := m.a.db.QueryRow("SELECT scopes FROM mcp_policies WHERE tenant=? AND user=?", in.Tenant, p.ID).Scan(&allowed)
	if err != nil || actorErr != nil || policyErr != nil || !scopeSubset(scope, request.Scope) || !scopeSubset(scope, allowed) || (actor.Role == "viewer" && (hasScope(scope, scopeDiagnostic) || hasScope(scope, scopeTerminal))) {
		m.a.mu.Unlock()
		c.Status(403)
		return
	}
	grant := randomID()
	now := time.Now()
	expires := now.Add(oauthGrantLifetime).Unix()
	tx, err := m.a.db.Begin()
	if err == nil {
		defer tx.Rollback()
		_, err = tx.Exec("DELETE FROM oauth_requests WHERE hash=?", digest(in.Request))
		if err == nil {
			_, err = tx.Exec("INSERT INTO oauth_grants(id,user,tenant,client,scopes,resource,expires,created) VALUES(?,?,?,?,?,?,?,?)", grant, p.ID, in.Tenant, m.clientID, scope, m.resource, expires, now.Unix())
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO sessions(hash,user,tenant,expires) VALUES(?,?,?,?)", oauthSession(grant), p.ID, in.Tenant, expires)
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", now.Unix(), p.ID, in.Tenant, "mcp-authorize", grant)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	m.a.mu.Unlock()
	if err != nil {
		c.Status(503)
		return
	}
	ti, err := m.oauth.GetAuthorizeToken(c.Request.Context(), &oauthserver.AuthorizeRequest{ResponseType: oauth2.Code, ClientID: m.clientID, UserID: grant, RedirectURI: request.Redirect, Scope: scope, CodeChallenge: request.Challenge, CodeChallengeMethod: oauth2.CodeChallengeS256, Request: c.Request})
	if err != nil {
		m.a.mu.Lock()
		m.revokeGrantLocked(grant)
		m.a.mu.Unlock()
		c.Status(503)
		return
	}
	values.Set("code", ti.GetCode())
	redirect.RawQuery = values.Encode()
	c.JSON(200, gin.H{"redirect": redirect.String()})
}

func oauthForm(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if media != "application/x-www-form-urlencoded" || err != nil || c.Request.URL.RawQuery != "" {
		c.Status(400)
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	if c.Request.ParseForm() != nil {
		c.Status(400)
		return false
	}
	for _, v := range c.Request.PostForm {
		if len(v) != 1 {
			c.Status(400)
			return false
		}
	}
	return true
}

func (m *terminalMCP) token(c *gin.Context) {
	if !oauthForm(c) {
		return
	}
	if c.Request.Host != m.host || c.PostForm("resource") != m.resource || c.PostForm("client_id") != m.clientID || c.GetHeader("Authorization") != "" || c.PostForm("client_secret") != "" {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	m.exchangeMu.Lock()
	defer m.exchangeMu.Unlock()
	gt, tgr, err := m.oauth.ValidationTokenRequest(c.Request)
	if err != nil || (gt != oauth2.AuthorizationCode && gt != oauth2.Refreshing) {
		c.JSON(400, gin.H{"error": "invalid_request"})
		return
	}
	if gt == oauth2.AuthorizationCode {
		v := tgr.CodeVerifier
		if len(v) < 43 || len(v) > 128 || strings.IndexFunc(v, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r))
		}) >= 0 {
			c.JSON(400, gin.H{"error": "invalid_grant"})
			return
		}
	} else {
		old, e := m.store.GetByRefresh(c.Request.Context(), tgr.Refresh)
		if e != nil {
			// Reusing a rotated refresh token revokes its entire consent grant.
			m.a.mu.Lock()
			var grant string
			if m.a.db.QueryRow("SELECT grant_id FROM oauth_spent_refresh WHERE hash=?", digest(tgr.Refresh)).Scan(&grant) == nil {
				m.revokeGrantLocked(grant)
			}
			m.a.mu.Unlock()
			c.JSON(400, gin.H{"error": "invalid_grant"})
			return
		}
		if old.GetClientID() != tgr.ClientID || (tgr.Scope != "" && tgr.Scope != old.GetScope()) {
			c.JSON(400, gin.H{"error": "invalid_grant"})
			return
		}
	}
	ti, err := m.oauth.GetAccessToken(c.Request.Context(), gt, tgr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid_grant"})
		return
	}
	c.JSON(200, m.oauth.GetTokenData(ti))
}

func (m *terminalMCP) revokeToken(c *gin.Context) {
	if !oauthForm(c) {
		return
	}
	if c.Request.Host != m.host || c.PostForm("client_id") != m.clientID {
		c.Status(400)
		return
	}
	m.exchangeMu.Lock()
	defer m.exchangeMu.Unlock()
	m.a.mu.Lock()
	defer m.a.mu.Unlock()
	var grant string
	if m.a.db.QueryRow("SELECT grant_id FROM oauth_tokens WHERE access_hash=? OR refresh_hash=?", digest(c.PostForm("token")), digest(c.PostForm("token"))).Scan(&grant) == nil {
		if m.revokeGrantLocked(grant) != nil {
			c.Status(503)
			return
		}
	}
	c.Status(200)
}

func (m *terminalMCP) verifyToken(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	if len(token) != 64 {
		return nil, auth.ErrInvalidToken
	}
	ti, err := m.manager.LoadAccessToken(ctx, token)
	if err != nil {
		return nil, auth.ErrInvalidToken
	}
	m.a.mu.Lock()
	p, err := m.grantLocked(ti.GetUserID(), ti.GetScope())
	m.a.mu.Unlock()
	if err != nil {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: ti.GetUserID(), Scopes: strings.Fields(ti.GetScope()), Expiration: ti.GetAccessCreateAt().Add(ti.GetAccessExpiresIn()), Extra: map[string]any{"actor": p}}, nil
}

func (m *terminalMCP) listGrants(c *gin.Context) {
	m.a.mu.Lock()
	defer m.a.mu.Unlock()
	p, err := m.a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	rows, err := m.a.db.Query("SELECT g.id,g.tenant,g.client,g.scopes,g.created,g.expires,(g.revoked OR NOT EXISTS(SELECT 1 FROM sessions s WHERE s.hash='mcp:'||g.id AND s.expires>?)) FROM oauth_grants g WHERE g.user=? ORDER BY g.created DESC LIMIT 100", time.Now().Unix(), p.ID)
	if err != nil {
		c.Status(503)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, tenant, client, scopes string
		var created, expires int64
		var revoked bool
		if rows.Scan(&id, &tenant, &client, &scopes, &created, &expires, &revoked) != nil {
			c.Status(503)
			return
		}
		items = append(items, gin.H{"id": id, "tenant_id": tenant, "client_id": client, "scopes": strings.Fields(scopes), "created": created, "expires": expires, "revoked": revoked})
	}
	if rows.Err() != nil {
		c.Status(503)
		return
	}
	c.JSON(200, gin.H{"grants": items, "url": m.resource, "client_id": m.clientID})
}
func (m *terminalMCP) deleteGrant(c *gin.Context) {
	m.a.mu.Lock()
	defer m.a.mu.Unlock()
	var id string
	p, err := m.a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	if m.a.db.QueryRow("SELECT id FROM oauth_grants WHERE id=? AND user=?", c.Param("id"), p.ID).Scan(&id) != nil {
		c.Status(404)
		return
	}
	if m.revokeGrantLocked(id) != nil || m.a.auditLocked(p, "mcp-revoke", id) != nil {
		c.Status(503)
		return
	}
	c.Status(204)
}

func (m *terminalMCP) listPolicies(c *gin.Context) {
	m.a.mu.Lock()
	defer m.a.mu.Unlock()
	p, err := m.a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	if p.Role != "admin" || p.Tenant == "" {
		c.Status(403)
		return
	}
	rows, err := m.a.db.Query("SELECT user,scopes FROM mcp_policies WHERE tenant=?", p.Tenant)
	if err != nil {
		c.Status(503)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var user, scopes string
		if rows.Scan(&user, &scopes) != nil {
			c.Status(503)
			return
		}
		items = append(items, gin.H{"user_id": user, "scopes": strings.Fields(scopes)})
	}
	if rows.Err() != nil {
		c.Status(503)
		return
	}
	c.JSON(200, items)
}
func (m *terminalMCP) setPolicy(c *gin.Context) {
	var in struct {
		User   string   `json:"user_id"`
		Scopes []string `json:"scopes"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	m.a.mu.Lock()
	defer m.a.mu.Unlock()
	p, err := m.a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	if p.Role != "admin" || p.Tenant == "" {
		c.Status(403)
		return
	}
	var admin bool
	if m.a.db.QueryRow("SELECT admin FROM users WHERE id=? AND disabled=0", in.User).Scan(&admin) != nil {
		c.Status(404)
		return
	}
	var role string
	if !admin && m.a.db.QueryRow("SELECT role FROM members WHERE tenant=? AND user=?", p.Tenant, in.User).Scan(&role) != nil {
		c.Status(404)
		return
	}
	scopes := ""
	if len(in.Scopes) > 0 {
		scopes, err = normalizeScopes(strings.Join(in.Scopes, " "))
		if err != nil || (role == "viewer" && (hasScope(scopes, scopeDiagnostic) || hasScope(scopes, scopeTerminal))) {
			c.Status(400)
			return
		}
	}
	tx, err := m.a.db.Begin()
	if err != nil {
		c.Status(503)
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO mcp_policies(tenant,user,scopes) VALUES(?,?,?) ON CONFLICT(tenant,user) DO UPDATE SET scopes=excluded.scopes", p.Tenant, in.User, scopes)
	if err == nil {
		_, err = tx.Exec("UPDATE oauth_grants SET revoked=1 WHERE tenant=? AND user=?", p.Tenant, in.User)
	}
	if err == nil {
		_, err = tx.Exec("DELETE FROM sessions WHERE hash IN (SELECT 'mcp:'||id FROM oauth_grants WHERE tenant=? AND user=?)", p.Tenant, in.User)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, "mcp-policy", in.User)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		c.Status(503)
		return
	}
	// Policy, consent invalidation and audit commit together. Existing browser
	// sessions remain usable; only MCP leases are closed by an MCP policy edit.
	for key, lease := range m.a.live {
		if lease.User == in.User && lease.Tenant == p.Tenant && strings.HasPrefix(lease.Session, "mcp:") {
			lease.revoked.Store(true)
			delete(m.a.live, key)
			go lease.close()
		}
	}
	c.Status(204)
}

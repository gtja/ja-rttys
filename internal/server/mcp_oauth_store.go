package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/go-oauth2/oauth2/v4"
	"github.com/go-oauth2/oauth2/v4/models"
)

// OAuth grants use a separate, unguessable session namespace. A browser cookie
// hashes to hex and cannot address these sessions. Existing account, membership,
// password-change and device revocation checks remain authoritative.
func oauthSession(grant string) string { return "mcp:" + grant }

func migrateTerminalMCP(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 4 {
		return tx.Commit()
	}
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS mcp_policies(tenant TEXT NOT NULL REFERENCES tenants(id), user TEXT NOT NULL REFERENCES users(id), scopes TEXT NOT NULL, PRIMARY KEY(tenant,user));
CREATE TABLE IF NOT EXISTS oauth_requests(hash TEXT PRIMARY KEY, payload TEXT NOT NULL, session TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS oauth_grants(id TEXT PRIMARY KEY, user TEXT NOT NULL REFERENCES users(id), tenant TEXT NOT NULL REFERENCES tenants(id), client TEXT NOT NULL, scopes TEXT NOT NULL, resource TEXT NOT NULL, expires INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS oauth_grant_user ON oauth_grants(user,tenant);
CREATE TABLE IF NOT EXISTS oauth_tokens(id INTEGER PRIMARY KEY, code_hash TEXT UNIQUE, access_hash TEXT UNIQUE, refresh_hash TEXT UNIQUE, grant_id TEXT NOT NULL REFERENCES oauth_grants(id), expires INTEGER NOT NULL, body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS oauth_spent_refresh(hash TEXT PRIMARY KEY, grant_id TEXT NOT NULL REFERENCES oauth_grants(id), expires INTEGER NOT NULL);
PRAGMA user_version=4;`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type oauthSQLStore struct{ m *terminalMCP }

// Store only token digests. OAuth library metadata contains no bearer material.
// Restore only the credential supplied by the caller when resolving a digest.
func (s *oauthSQLStore) Create(ctx context.Context, info oauth2.TokenInfo) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	t := models.NewToken()
	if err = json.Unmarshal(b, t); err != nil {
		return err
	}
	code, access, refresh := t.Code, t.Access, t.Refresh
	t.Code, t.Access, t.Refresh = "", "", ""
	b, err = json.Marshal(t)
	if err != nil {
		return err
	}
	s.m.a.mu.Lock()
	defer s.m.a.mu.Unlock()
	if _, err = s.m.grantLocked(t.UserID, t.Scope); err != nil {
		return errDenied
	}
	expires := t.AccessCreateAt.Add(t.AccessExpiresIn).Unix()
	if code != "" {
		expires = t.CodeCreateAt.Add(t.CodeExpiresIn).Unix()
	}
	if refresh != "" {
		expires = t.RefreshCreateAt.Add(t.RefreshExpiresIn).Unix()
	}
	_, err = s.m.a.db.ExecContext(ctx, "INSERT INTO oauth_tokens(code_hash,access_hash,refresh_hash,grant_id,expires,body) VALUES(?,?,?,?,?,?)", tokenDigest(code), tokenDigest(access), tokenDigest(refresh), t.UserID, expires, string(b))
	return err
}

func tokenDigest(v string) any {
	if v == "" {
		return nil
	}
	return digest(v)
}

func (s *oauthSQLStore) get(ctx context.Context, column, value string) (oauth2.TokenInfo, error) {
	s.m.a.mu.Lock()
	defer s.m.a.mu.Unlock()
	var body string
	if err := s.m.a.db.QueryRowContext(ctx, "SELECT body FROM oauth_tokens WHERE "+column+"=?", digest(value)).Scan(&body); err != nil {
		return nil, errDenied
	}
	t := models.NewToken()
	if json.Unmarshal([]byte(body), t) != nil {
		return nil, errDenied
	}
	if _, err := s.m.grantLocked(t.UserID, t.Scope); err != nil {
		return nil, errDenied
	}
	if t.Extension.Get("resource") != s.m.resource {
		return nil, errDenied
	}
	switch column {
	case "code_hash":
		t.Code = value
	case "access_hash":
		t.Access = value
	case "refresh_hash":
		t.Refresh = value
	}
	return t, nil
}
func (s *oauthSQLStore) GetByCode(ctx context.Context, v string) (oauth2.TokenInfo, error) {
	return s.get(ctx, "code_hash", v)
}
func (s *oauthSQLStore) GetByAccess(ctx context.Context, v string) (oauth2.TokenInfo, error) {
	return s.get(ctx, "access_hash", v)
}
func (s *oauthSQLStore) GetByRefresh(ctx context.Context, v string) (oauth2.TokenInfo, error) {
	return s.get(ctx, "refresh_hash", v)
}
func (s *oauthSQLStore) RemoveByCode(ctx context.Context, v string) error {
	s.m.a.mu.Lock()
	defer s.m.a.mu.Unlock()
	r, err := s.m.a.db.ExecContext(ctx, "DELETE FROM oauth_tokens WHERE code_hash=?", digest(v))
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n != 1 {
		return errDenied
	}
	return err
}
func (s *oauthSQLStore) RemoveByAccess(ctx context.Context, v string) error {
	s.m.a.mu.Lock()
	defer s.m.a.mu.Unlock()
	_, err := s.m.a.db.ExecContext(ctx, "UPDATE oauth_tokens SET access_hash=NULL WHERE access_hash=?", digest(v))
	return err
}
func (s *oauthSQLStore) RemoveByRefresh(ctx context.Context, v string) error {
	s.m.a.mu.Lock()
	defer s.m.a.mu.Unlock()
	tx, err := s.m.a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO oauth_spent_refresh SELECT refresh_hash,grant_id,expires FROM oauth_tokens WHERE refresh_hash=?", digest(v)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE oauth_tokens SET refresh_hash=NULL WHERE refresh_hash=?", digest(v)); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *terminalMCP) grantLocked(id, scopes string) (*Actor, error) {
	var current, resource, client string
	var expires int64
	if m.a.db.QueryRow("SELECT scopes,resource,client,expires FROM oauth_grants WHERE id=? AND revoked=0", id).Scan(&current, &resource, &client, &expires) != nil || expires <= time.Now().Unix() || resource != m.resource || client != m.clientID || current != scopes {
		return nil, errDenied
	}
	p, err := m.a.actorLocked(oauthSession(id))
	if err != nil {
		return nil, errDenied
	}
	var allowed string
	if m.a.db.QueryRow("SELECT scopes FROM mcp_policies WHERE tenant=? AND user=?", p.Tenant, p.ID).Scan(&allowed) != nil || !scopeSubset(current, allowed) {
		return nil, errDenied
	}
	if p.Role == "viewer" && (hasScope(scopes, scopeDiagnostic) || hasScope(scopes, scopeTerminal)) {
		return nil, errDenied
	}
	return p, nil
}

func (m *terminalMCP) revokeGrantLocked(id string) error {
	tx, err := m.a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE oauth_grants SET revoked=1 WHERE id=?", id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE hash=?", oauthSession(id)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for key, l := range m.a.live {
		if l.Session == oauthSession(id) {
			l.revoked.Store(true)
			delete(m.a.live, key)
			go l.close()
		}
	}
	return nil
}

func (m *terminalMCP) pruneLocked() {
	now := time.Now().Unix()
	m.a.db.Exec("DELETE FROM oauth_requests WHERE expires<=?", now)
	m.a.db.Exec("DELETE FROM oauth_tokens WHERE expires<=?", now)
	m.a.db.Exec("DELETE FROM oauth_spent_refresh WHERE expires<=?", now)
}

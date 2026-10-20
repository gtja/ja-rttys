package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// The single-instance gate serializes policy changes with connection admission.
// A revoked lease blocks data immediately; transport closure is asynchronous.
type TenantAuth struct {
	mu            sync.Mutex
	db            *sql.DB
	srv           *RttyServer
	live          map[string]*accessLease
	attempts      map[string]loginAttempt
	passwordSlots chan struct{}
}
type loginAttempt struct {
	Count int
	Until time.Time
}
type accessLease struct {
	Session, User, Tenant, Device string
	Expires                       int64
	revoked                       atomic.Bool
	close                         func()
}

func (l *accessLease) valid() bool {
	return l != nil && !l.revoked.Load() && time.Now().Unix() < l.Expires
}

type Actor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Admin    bool   `json:"platform_admin"`
	Tenant   string `json:"tenant_id"`
	Role     string `json:"role"`
	Session  string `json:"-"`
	Expires  int64  `json:"-"`
}
type TenantDevice struct {
	ID         string   `json:"id"`
	Tenant     string   `json:"tenant_id"`
	WireID     string   `json:"wire_id"`
	Group      string   `json:"group"`
	Name       string   `json:"name"`
	Disabled   bool     `json:"disabled"`
	WebTargets []string `json:"web_targets"`
}

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

var errDenied = errors.New("access denied")

func openTenantAuth(srv *RttyServer) (*TenantAuth, error) {
	cfg := srv.cfg
	if err := os.MkdirAll(filepath.Dir(cfg.TenantDB), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(cfg.TenantDB, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(cfg.TenantDB, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", cfg.TenantDB)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	schema := `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=DELETE; PRAGMA locking_mode=EXCLUSIVE;
 CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL, password BLOB NOT NULL, admin INTEGER NOT NULL DEFAULT 0, disabled INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS tenants(id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL, disabled INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS members(tenant TEXT REFERENCES tenants(id), user TEXT REFERENCES users(id), role TEXT NOT NULL CHECK(role IN ('admin','operator','viewer')), PRIMARY KEY(tenant,user));
 CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY, tenant TEXT NOT NULL REFERENCES tenants(id), wire_id TEXT NOT NULL, grp TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, key_hash TEXT NOT NULL UNIQUE, disabled INTEGER NOT NULL DEFAULT 0, web_targets TEXT NOT NULL DEFAULT '[]', UNIQUE(tenant,wire_id));
 CREATE TABLE IF NOT EXISTS grants(tenant TEXT NOT NULL REFERENCES tenants(id), user TEXT NOT NULL REFERENCES users(id), device TEXT NOT NULL DEFAULT '', grp TEXT NOT NULL DEFAULT '', PRIMARY KEY(tenant,user,device,grp));
 CREATE TABLE IF NOT EXISTS sessions(hash TEXT PRIMARY KEY,user TEXT NOT NULL REFERENCES users(id),tenant TEXT NOT NULL DEFAULT '',expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY, at INTEGER NOT NULL,user TEXT,tenant TEXT,action TEXT NOT NULL,resource TEXT,result TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS device_tenant ON devices(tenant);
 CREATE INDEX IF NOT EXISTS session_user ON sessions(user);`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	a := &TenantAuth{db: db, srv: srv, live: map[string]*accessLease{}, attempts: map[string]loginAttempt{}, passwordSlots: make(chan struct{}, 4)}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil {
		db.Close()
		return nil, err
	}
	if count == 0 {
		secret, readErr := os.ReadFile(cfg.BootstrapPasswordFile)
		if readErr != nil {
			db.Close()
			return nil, fmt.Errorf("empty account database requires bootstrap password file: %w", readErr)
		}
		pw := strings.TrimSpace(string(secret))
		if len(pw) < 12 || len(pw) > 72 {
			db.Close()
			return nil, errors.New("bootstrap password must be 12-72 bytes")
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(pw), 12)
		if hashErr != nil {
			db.Close()
			return nil, hashErr
		}
		if _, err = db.Exec("INSERT INTO users(id,username,password,admin) VALUES(?,?,?,1)", randomID(), "admin", hash); err != nil {
			db.Close()
			return nil, err
		}
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			a.mu.Lock()
			for id, l := range a.live {
				if !l.valid() {
					l.revoked.Store(true)
					delete(a.live, id)
					go l.close()
				}
			}
			a.db.Exec("DELETE FROM sessions WHERE expires<=?", time.Now().Unix())
			a.mu.Unlock()
		}
	}()
	return a, nil
}

func (a *TenantAuth) actorLocked(sid string) (*Actor, error) {
	p := &Actor{Session: sid}
	err := a.db.QueryRow(`SELECT u.id,u.username,u.admin,s.tenant,s.expires FROM sessions s JOIN users u ON u.id=s.user WHERE s.hash=? AND s.expires>? AND u.disabled=0`, sid, time.Now().Unix()).Scan(&p.ID, &p.Username, &p.Admin, &p.Tenant, &p.Expires)
	if err != nil {
		return nil, errDenied
	}
	if p.Tenant != "" {
		var disabled bool
		if err = a.db.QueryRow("SELECT disabled FROM tenants WHERE id=?", p.Tenant).Scan(&disabled); err != nil || disabled {
			return nil, errDenied
		}
		if p.Admin {
			p.Role = "admin"
		} else if err = a.db.QueryRow("SELECT role FROM members WHERE tenant=? AND user=?", p.Tenant, p.ID).Scan(&p.Role); err != nil {
			return nil, errDenied
		}
	} else if !p.Admin {
		return nil, errDenied
	}
	return p, nil
}
func (a *TenantAuth) actor(sid string) (*Actor, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.actorLocked(sid)
}
func (a *TenantAuth) deviceLocked(id string) (*TenantDevice, error) {
	d := new(TenantDevice)
	var targets string
	err := a.db.QueryRow("SELECT id,tenant,wire_id,grp,name,disabled,web_targets FROM devices WHERE id=?", id).Scan(&d.ID, &d.Tenant, &d.WireID, &d.Group, &d.Name, &d.Disabled, &targets)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(targets), &d.WebTargets); err != nil {
		return nil, err
	}
	return d, nil
}
func (a *TenantAuth) allowedLocked(p *Actor, d *TenantDevice, operate bool) bool {
	if p.Tenant == "" || p.Tenant != d.Tenant || d.Disabled {
		return false
	}
	if p.Role == "admin" {
		return true
	}
	if p.Role == "viewer" {
		return !operate
	}
	if p.Role != "operator" {
		return false
	}
	var n int
	err := a.db.QueryRow("SELECT count(*) FROM grants WHERE tenant=? AND user=? AND (device=? OR (device='' AND grp=?))", p.Tenant, p.ID, d.ID, d.Group).Scan(&n)
	return err == nil && n > 0
}
func (a *TenantAuth) access(sid, id string, operate bool) (*Actor, *TenantDevice, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(sid)
	if err != nil {
		return nil, nil, err
	}
	d, err := a.deviceLocked(id)
	if err != nil || d.Tenant != p.Tenant {
		return p, nil, sql.ErrNoRows
	}
	if !a.allowedLocked(p, d, operate) {
		return p, d, errDenied
	}
	return p, d, nil
}
func (a *TenantAuth) attach(sid, id string, close func()) (*accessLease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(sid)
	if err != nil {
		return nil, err
	}
	d, err := a.deviceLocked(id)
	if err != nil || !a.allowedLocked(p, d, true) {
		return nil, errDenied
	}
	l := &accessLease{Session: sid, User: p.ID, Tenant: p.Tenant, Device: id, Expires: p.Expires, close: close}
	if err := a.auditLocked(p, "access-open", id); err != nil {
		return nil, err
	}
	a.live[randomID()] = l
	return l, nil
}
func (a *TenantAuth) detach(l *accessLease) {
	if l == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.auditLocked(&Actor{ID: l.User, Tenant: l.Tenant}, "access-close", l.Device)
	for id, v := range a.live {
		if v == l {
			delete(a.live, id)
			break
		}
	}
}
func (a *TenantAuth) revokeLocked(user, tenant, device string) {
	for id, l := range a.live {
		if (user == "" || user == l.User) && (tenant == "" || tenant == l.Tenant) && (device == "" || device == l.Device) {
			l.revoked.Store(true)
			delete(a.live, id)
			go l.close()
		}
	}
}
func (a *TenantAuth) auditLocked(p *Actor, action, resource string) error {
	_, err := a.db.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, action, resource)
	return err
}
func (a *TenantAuth) register(dev *Device) byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(dev.token) < 32 {
		return devRegErrInvalidToken
	}
	var id, tenant, wire, hash string
	var disabled, tenantDisabled bool
	err := a.db.QueryRow(`SELECT d.id,d.tenant,d.wire_id,d.key_hash,d.disabled,t.disabled FROM devices d JOIN tenants t ON d.tenant=t.id WHERE d.key_hash=?`, digest(dev.token)).Scan(&id, &tenant, &wire, &hash, &disabled, &tenantDisabled)
	if err != nil || disabled || tenantDisabled || wire != dev.id || subtle.ConstantTimeCompare([]byte(hash), []byte(digest(dev.token))) != 1 {
		return devRegErrInvalidToken
	}
	// Never trust a device-supplied group as its tenant or routing identity.
	dev.wireID = dev.id
	dev.id = id
	dev.group = tenant
	dev.token = ""
	if !a.srv.AddDevice(dev) {
		return devRegErrIdConflicting
	}
	return 0
}

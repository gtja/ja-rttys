package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func tenantSession(c *gin.Context) string { v, _ := c.Cookie("rt_sid"); return digest(v) }
func tenantOrigin(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Host == c.Request.Host
}
func tenantJSON(c *gin.Context, v any) bool {
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		c.AbortWithStatus(415)
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		c.AbortWithStatus(400)
		return false
	}
	if dec.Decode(new(any)) != io.EOF {
		c.AbortWithStatus(400)
		return false
	}
	return true
}
func (a *TenantAuth) signin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !tenantOrigin(c) {
		c.Status(403)
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	if len(in.Username) > 128 || len(in.Password) > 72 {
		c.Status(401)
		return
	}
	a.mu.Lock()
	ip := c.RemoteIP()
	accountKey := ip + "\x00" + in.Username
	now := time.Now()
	if len(a.attempts) > 10000 {
		for key, x := range a.attempts {
			if x.Until.Before(now) {
				delete(a.attempts, key)
			}
		}
	}
	if len(a.attempts) > 20000 {
		a.mu.Unlock()
		c.Status(429)
		return
	}
	for key, limit := range map[string]int{"ip:" + ip: 100, accountKey: 10} {
		v := a.attempts[key]
		if v.Until.Before(now) {
			window := time.Minute
			if key == accountKey {
				window = 5 * time.Minute
			}
			v = loginAttempt{Until: now.Add(window)}
		}
		if v.Count >= limit {
			a.mu.Unlock()
			c.Status(429)
			return
		}
		v.Count++
		a.attempts[key] = v
	}
	var id string
	var hash []byte
	var admin bool
	err := a.db.QueryRow("SELECT id,password,admin FROM users WHERE username=? AND disabled=0", in.Username).Scan(&id, &hash, &admin)
	a.mu.Unlock()
	if err != nil {
		c.Status(401)
		return
	}
	select {
	case a.passwordSlots <- struct{}{}:
	default:
		c.Status(429)
		return
	}
	err = bcrypt.CompareHashAndPassword(hash, []byte(in.Password))
	<-a.passwordSlots
	if err != nil {
		c.Status(401)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var current []byte
	if a.db.QueryRow("SELECT password FROM users WHERE id=? AND disabled=0", id).Scan(&current) != nil || !bytes.Equal(current, hash) {
		c.Status(401)
		return
	}
	tenant := ""
	if !admin {
		err = a.db.QueryRow("SELECT m.tenant FROM members m JOIN tenants t ON t.id=m.tenant WHERE m.user=? AND t.disabled=0 ORDER BY m.tenant LIMIT 1", id).Scan(&tenant)
		if err != nil {
			c.Status(401)
			return
		}
	}
	token := randomID()
	expires := now.Add(8 * time.Hour).Unix()
	if _, err = a.db.Exec("INSERT INTO sessions(hash,user,tenant,expires) VALUES(?,?,?,?)", digest(token), id, tenant, expires); err != nil {
		c.Status(503)
		return
	}
	delete(a.attempts, accountKey)
	http.SetCookie(c.Writer, &http.Cookie{Name: "rt_sid", Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600})
	c.JSON(200, gin.H{"username": in.Username, "tenant_id": tenant})
}
func (a *TenantAuth) middleware(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !tenantOrigin(c) {
		c.AbortWithStatus(403)
		return
	}
	p, err := a.actor(tenantSession(c))
	if err != nil {
		c.AbortWithStatus(401)
		return
	}
	c.Set("principal", p)
	path := c.FullPath()
	if strings.Contains(path, "/cmd/") || strings.Contains(path, "/shares") || strings.Contains(path, "/serial-") || c.Query("mode") == "serial" {
		c.AbortWithStatus(403)
		return
	}
	if id := c.Param("devid"); id != "" {
		operate := !strings.Contains(path, "/dev/")
		_, d, err := a.access(p.Session, id, operate)
		if err != nil {
			if d == nil {
				c.AbortWithStatus(404)
			} else {
				c.AbortWithStatus(403)
			}
			return
		}
		if g := c.Query("group"); g != "" && !deviceHasGroup(d, g) && g != p.Tenant {
			c.AbortWithStatus(404)
			return
		}
		if g := c.Param("group"); g != "" && !deviceHasGroup(d, g) && g != p.Tenant {
			c.AbortWithStatus(404)
			return
		}
		q := c.Request.URL.Query()
		q.Set("group", p.Tenant)
		c.Request.URL.RawQuery = q.Encode()
		for i := range c.Params {
			if c.Params[i].Key == "group" {
				c.Params[i].Value = p.Tenant
			}
		}
	}
}
func principal(c *gin.Context) *Actor { return c.MustGet("principal").(*Actor) }
func requestGroup(c *gin.Context) string {
	if p, ok := c.Get("principal"); ok {
		return p.(*Actor).Tenant
	}
	return c.Query("group")
}
func (a *TenantAuth) routes(r *gin.Engine) {
	r.POST("/api/signin", a.signin)
	api := r.Group("/api", a.middleware)
	api.GET("/alive", func(c *gin.Context) { c.Status(200) })
	api.GET("/me", a.me)
	api.POST("/password", a.changePassword)
	api.POST("/signout", func(c *gin.Context) {
		a.mu.Lock()
		defer a.mu.Unlock()
		p := principal(c)
		if _, err := a.db.Exec("DELETE FROM sessions WHERE hash=?", p.Session); err != nil {
			c.Status(503)
			return
		}
		for id, l := range a.live {
			if l.Session == p.Session {
				l.revoked.Store(true)
				delete(a.live, id)
				go l.close()
			}
		}
		http.SetCookie(c.Writer, &http.Cookie{Name: "rt_sid", Path: "/", Secure: true, HttpOnly: true, MaxAge: -1})
		c.Status(200)
	})
	api.POST("/tenant", a.switchTenant)
	api.GET("/manage/snapshot", a.snapshot)
	api.POST("/manage/tenants", a.manage)
	api.PATCH("/manage/tenants/:id", a.manage)
	api.POST("/manage/users", a.manage)
	api.PATCH("/manage/users/:id", a.manage)
	api.PUT("/manage/members", a.manage)
	api.DELETE("/manage/members/:id", a.manage)
	api.POST("/manage/devices", a.manage)
	api.PATCH("/manage/devices/:id", a.manage)
	api.POST("/manage/devices/:id/rotate", a.manage)
	api.PUT("/manage/grants", a.manage)
	api.POST("/manage/groups", a.manageGroups)
	api.PATCH("/manage/groups/:name", a.manageGroups)
	api.DELETE("/manage/groups/:name", a.manageGroups)
	api.PUT("/manage/devices/:id/groups", a.manageGroups)
}
func (a *TenantAuth) me(c *gin.Context) {
	p := principal(c)
	a.mu.Lock()
	defer a.mu.Unlock()
	query := "SELECT t.id,t.name,m.role FROM tenants t JOIN members m ON m.tenant=t.id WHERE m.user=? AND t.disabled=0"
	args := []any{p.ID}
	if p.Admin {
		query = "SELECT id,name,'admin' FROM tenants WHERE disabled=0"
		args = nil
	}
	rows, err := a.db.Query(query, args...)
	if err != nil {
		c.Status(503)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, name, role string
		if rows.Scan(&id, &name, &role) != nil {
			c.Status(503)
			return
		}
		items = append(items, gin.H{"id": id, "name": name, "role": role})
	}
	c.JSON(200, gin.H{"user": p, "tenants": items})
}
func (a *TenantAuth) switchTenant(c *gin.Context) {
	var in struct {
		Tenant string `json:"tenant_id"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM tenants WHERE id=? AND disabled=0", in.Tenant).Scan(&n)
	if n != 1 {
		c.Status(403)
		return
	}
	if !p.Admin {
		a.db.QueryRow("SELECT count(*) FROM members WHERE tenant=? AND user=?", in.Tenant, p.ID).Scan(&n)
		if n != 1 {
			c.Status(403)
			return
		}
	}
	if _, err = a.db.Exec("UPDATE sessions SET tenant=? WHERE hash=?", in.Tenant, p.Session); err != nil {
		c.Status(503)
		return
	}
	for id, l := range a.live {
		if l.Session == p.Session {
			l.revoked.Store(true)
			delete(a.live, id)
			go l.close()
		}
	}
	c.Status(200)
}
func (a *TenantAuth) list(c *gin.Context, kind string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	rows, err := a.db.Query("SELECT id FROM devices WHERE tenant=? AND disabled=0", p.Tenant)
	if err != nil {
		c.Status(503)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	items := []gin.H{}
	groups := map[string]bool{}
	for _, id := range ids {
		d, err := a.deviceLocked(id)
		if err != nil {
			c.Status(503)
			return
		}
		if !a.allowedLocked(p, d, false) {
			continue
		}
		for _, g := range d.Groups {
			groups[g] = true
		}
		if kind == "devs" && c.Query("group") != "" && !deviceHasGroup(d, c.Query("group")) {
			continue
		}
		info := gin.H{"id": id, "wire_id": d.WireID, "group": d.Group, "groups": d.Groups, "description": d.Name, "can_operate": a.allowedLocked(p, d, true), "online": false, "proto": 0, "connected": 0, "uptime": 0, "ipaddr": ""}
		if dev := a.srv.GetDevice(p.Tenant, id); dev != nil {
			info["online"] = true
			info["proto"] = dev.proto
			info["connected"] = uint32(time.Now().Unix() - dev.timestamp)
			info["uptime"] = dev.uptime
			info["ipaddr"] = dev.conn.RemoteAddr().String()
		}
		items = append(items, info)
	}
	switch kind {
	case "groups":
		g := []string{}
		for group := range groups {
			g = append(g, group)
		}
		c.JSON(200, g)
	case "counts":
		c.JSON(200, gin.H{"count": len(items)})
	default:
		c.JSON(200, items)
	}
}

type manageInput struct {
	Name       string   `json:"name"`
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	Tenant     string   `json:"tenant_id"`
	User       string   `json:"user_id"`
	Role       string   `json:"role"`
	WireID     string   `json:"wire_id"`
	Group      string   `json:"group"`
	Device     string   `json:"device_id"`
	Access     bool     `json:"access"`
	Disabled   *bool    `json:"disabled"`
	WebTargets []string `json:"web_targets"`
}

func validName(s string) bool {
	return len(s) > 0 && len(s) <= 128 && !strings.ContainsAny(s, "\x00\r\n/\\")
}
func (a *TenantAuth) manage(c *gin.Context) {
	var in manageInput
	if c.Request.Method != "DELETE" && !tenantJSON(c, &in) {
		return
	}
	var passwordHash []byte
	if c.FullPath() == "/api/manage/users" || (c.FullPath() == "/api/manage/users/:id" && in.Disabled == nil) {
		initial := principal(c)
		if (!initial.Admin && initial.Role != "admin") || (c.FullPath() == "/api/manage/users/:id" && !initial.Admin) {
			c.Status(403)
			return
		}
		if len(in.Password) < 12 || len(in.Password) > 72 {
			c.Status(400)
			return
		}
		select {
		case a.passwordSlots <- struct{}{}:
		default:
			c.Status(429)
			return
		}
		var hashErr error
		passwordHash, hashErr = bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		<-a.passwordSlots
		if hashErr != nil {
			c.Status(503)
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	if !p.Admin && p.Role != "admin" {
		c.Status(403)
		return
	}
	targetTenant := in.Tenant
	if targetTenant == "" {
		targetTenant = p.Tenant
	}
	if !p.Admin && targetTenant != p.Tenant {
		c.Status(403)
		return
	}
	path := c.FullPath()
	id := c.Param("id")
	result := gin.H{"ok": true}
	status := 200
	fail := func(e error) bool {
		if e != nil {
			c.JSON(409, gin.H{"error": "operation rejected; check uniqueness and references"})
			return true
		}
		return false
	}
	switch {
	case path == "/api/manage/tenants":
		if !p.Admin {
			c.Status(403)
			return
		}
		if !validName(in.Name) {
			c.Status(400)
			return
		}
		id = randomID()
		_, err = a.db.Exec("INSERT INTO tenants(id,name) VALUES(?,?)", id, in.Name)
		status = 201
		result = gin.H{"id": id, "name": in.Name}
	case path == "/api/manage/tenants/:id":
		if !p.Admin || in.Disabled == nil {
			c.Status(403)
			return
		}
		_, err = a.db.Exec("UPDATE tenants SET disabled=? WHERE id=?", *in.Disabled, id)
		if fail(err) {
			return
		}
		a.revokeLocked("", id, "")
		a.db.Exec("DELETE FROM sessions WHERE tenant=?", id)
		a.disconnectTenantLocked(id)
	case path == "/api/manage/users":
		if !validName(in.Username) || len(in.Password) < 12 || len(in.Password) > 72 {
			c.Status(400)
			return
		}
		hash := passwordHash
		id = randomID()
		tx, e := a.db.Begin()
		if fail(e) {
			return
		}
		_, e = tx.Exec("INSERT INTO users(id,username,password) VALUES(?,?,?)", id, in.Username, hash)
		if e == nil && !p.Admin {
			_, e = tx.Exec("INSERT INTO members(tenant,user,role) VALUES(?,?,'operator')", p.Tenant, id)
		}
		if e != nil {
			tx.Rollback()
			fail(e)
			return
		}
		err = tx.Commit()
		status = 201
		result = gin.H{"id": id, "username": in.Username}
	case path == "/api/manage/users/:id":
		if !p.Admin || id == p.ID {
			c.Status(403)
			return
		}
		if in.Disabled != nil {
			_, err = a.db.Exec("UPDATE users SET disabled=? WHERE id=?", *in.Disabled, id)
		} else if len(in.Password) >= 12 && len(in.Password) <= 72 {
			_, err = a.db.Exec("UPDATE users SET password=? WHERE id=?", passwordHash, id)
		} else {
			c.Status(400)
			return
		}
		if fail(err) {
			return
		}
		a.db.Exec("DELETE FROM sessions WHERE user=?", id)
		a.revokeLocked(id, "", "")
	case path == "/api/manage/members":
		if targetTenant == "" || (in.Role != "admin" && in.Role != "operator" && in.Role != "viewer") {
			c.Status(400)
			return
		}
		if in.User == p.ID && !p.Admin {
			c.Status(403)
			return
		}
		_, err = a.db.Exec("INSERT INTO members(tenant,user,role) VALUES(?,?,?) ON CONFLICT(tenant,user) DO UPDATE SET role=excluded.role", targetTenant, in.User, in.Role)
		if fail(err) {
			return
		}
		a.db.Exec("DELETE FROM sessions WHERE user=? AND tenant=?", in.User, targetTenant)
		a.revokeLocked(in.User, targetTenant, "")
	case path == "/api/manage/members/:id":
		if p.Tenant == "" || id == p.ID {
			c.Status(403)
			return
		}
		tx, e := a.db.Begin()
		if fail(e) {
			return
		}
		_, e = tx.Exec("DELETE FROM members WHERE tenant=? AND user=?", p.Tenant, id)
		if e == nil {
			_, e = tx.Exec("DELETE FROM grants WHERE tenant=? AND user=?", p.Tenant, id)
		}
		if e == nil {
			_, e = tx.Exec("DELETE FROM sessions WHERE tenant=? AND user=?", p.Tenant, id)
		}
		if e != nil {
			tx.Rollback()
			fail(e)
			return
		}
		err = tx.Commit()
		if fail(err) {
			return
		}
		a.revokeLocked(id, p.Tenant, "")
	case path == "/api/manage/devices":
		if targetTenant == "" || !validName(in.WireID) || len(in.Group) > 128 {
			c.Status(400)
			return
		}
		if !validWebTargets(in.WebTargets) {
			c.Status(400)
			return
		}
		if in.Name == "" {
			in.Name = in.WireID
		}
		id = randomID()
		token := randomID()
		targets, _ := json.Marshal(in.WebTargets)
		if in.WebTargets == nil {
			targets = []byte("[]")
		}
		if in.Group != "" && !validName(in.Group) {
			c.Status(400)
			return
		}
		tx, e := a.db.Begin()
		if fail(e) {
			return
		}
		defer tx.Rollback()
		_, err = tx.Exec("INSERT INTO devices(id,tenant,wire_id,grp,name,key_hash,web_targets) VALUES(?,?,?,'',?,?,?)", id, targetTenant, in.WireID, in.Name, digest(token), string(targets))
		if err == nil && in.Group != "" {
			_, err = tx.Exec("INSERT OR IGNORE INTO tenant_groups(tenant,name) VALUES(?,?)", targetTenant, in.Group)
			if err == nil {
				_, err = tx.Exec("INSERT INTO device_groups(tenant,device,grp) VALUES(?,?,?)", targetTenant, id, in.Group)
			}
		}
		if err == nil {
			err = tx.Commit()
		}

		status = 201
		result = gin.H{"id": id, "wire_id": in.WireID, "token": token}
	case strings.HasPrefix(path, "/api/manage/devices/:id"):
		d, e := a.deviceLocked(id)
		if e != nil || d.Tenant != p.Tenant {
			c.Status(404)
			return
		}
		if strings.HasSuffix(path, "/rotate") {
			token := randomID()
			_, err = a.db.Exec("UPDATE devices SET key_hash=? WHERE id=?", digest(token), id)
			result = gin.H{"token": token}
		} else if in.Disabled != nil {
			_, err = a.db.Exec("UPDATE devices SET disabled=? WHERE id=?", *in.Disabled, id)
		} else {
			c.Status(400)
			return
		}
		if fail(err) {
			return
		}
		a.revokeLocked("", d.Tenant, id)
		if dev := a.srv.GetDevice(d.Tenant, id); dev != nil {
			go dev.Close(a.srv)
		}
	case path == "/api/manage/grants":
		if p.Tenant == "" {
			c.Status(400)
			return
		}
		var n int
		if e := a.db.QueryRow("SELECT count(*) FROM members WHERE tenant=? AND user=?", p.Tenant, in.User).Scan(&n); e != nil || n != 1 {
			c.Status(404)
			return
		}
		if in.Device != "" {
			d, e := a.deviceLocked(in.Device)
			if e != nil || d.Tenant != p.Tenant {
				c.Status(404)
				return
			}
			in.Group = ""
		} else {
			if !validName(in.Group) {
				c.Status(400)
				return
			}
			if a.db.QueryRow("SELECT count(*) FROM tenant_groups WHERE tenant=? AND name=?", p.Tenant, in.Group).Scan(&n) != nil || n != 1 {
				c.Status(404)
				return
			}
		}
		if in.Access {
			_, err = a.db.Exec("INSERT OR IGNORE INTO grants(tenant,user,device,grp) VALUES(?,?,?,?)", p.Tenant, in.User, in.Device, in.Group)
		} else {
			_, err = a.db.Exec("DELETE FROM grants WHERE tenant=? AND user=? AND device=? AND grp=?", p.Tenant, in.User, in.Device, in.Group)
		}
		if fail(err) {
			return
		}
		a.revalidateLocked(p.Tenant)
	default:
		c.Status(404)
		return
	}
	if fail(err) {
		return
	}
	if fail(a.auditLocked(p, c.Request.Method+" "+path, id)) {
		return
	}
	c.JSON(status, result)
}
func (a *TenantAuth) disconnectTenantLocked(tenant string) {
	g := a.srv.GetGroup(tenant, false)
	if g != nil {
		g.devices.Range(func(_, v any) bool { go v.(*Device).Close(a.srv); return true })
	}
}
func (a *TenantAuth) snapshot(c *gin.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	if !p.Admin && p.Role != "admin" {
		c.Status(403)
		return
	}
	// Generic rows are used only for these fixed, scoped administrative queries.
	query := func(q string, args ...any) ([]map[string]any, error) {
		rows, e := a.db.Query(q, args...)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		out := []map[string]any{}
		for rows.Next() {
			v := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range v {
				ptr[i] = &v[i]
			}
			if e = rows.Scan(ptr...); e != nil {
				return nil, e
			}
			item := map[string]any{}
			for i, col := range cols {
				item[col] = v[i]
			}
			out = append(out, item)
		}
		return out, rows.Err()
	}
	result := map[string]any{}
	queries := map[string]string{"groups": "SELECT name FROM tenant_groups WHERE tenant=? ORDER BY name", "device_groups": "SELECT device AS device_id,grp AS 'group' FROM device_groups WHERE tenant=? ORDER BY grp", "devices": "SELECT id,wire_id,grp AS 'group',name,disabled FROM devices WHERE tenant=?", "members": "SELECT u.id,u.username,u.disabled,m.role FROM members m JOIN users u ON u.id=m.user WHERE m.tenant=?", "grants": "SELECT user AS user_id,device AS device_id,grp AS 'group' FROM grants WHERE tenant=?", "audit": "SELECT at,user,action,resource,result FROM audit WHERE tenant=? ORDER BY id DESC LIMIT 100"}
	for name, q := range queries {
		v, e := query(q, p.Tenant)
		if e != nil {
			c.Status(503)
			return
		}
		result[name] = v
	}
	if p.Admin {
		v, e := query("SELECT id,username,disabled,admin FROM users")
		if e != nil {
			c.Status(503)
			return
		}
		result["users"] = v
		v, e = query("SELECT id,name,disabled FROM tenants")
		if e != nil {
			c.Status(503)
			return
		}
		result["tenants"] = v
	}
	c.JSON(200, result)
}

var _ = sql.ErrNoRows

package server

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (a *TenantAuth) changePassword(c *gin.Context) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	if len(in.New) < 12 || len(in.New) > 72 || len(in.Current) > 72 {
		c.Status(400)
		return
	}
	a.mu.Lock()
	p, err := a.actorLocked(tenantSession(c))
	if err != nil {
		a.mu.Unlock()
		c.Status(401)
		return
	}
	var hash []byte
	err = a.db.QueryRow("SELECT password FROM users WHERE id=?", p.ID).Scan(&hash)
	a.mu.Unlock()
	if err != nil {
		c.Status(503)
		return
	}
	select {
	case a.passwordSlots <- struct{}{}:
	default:
		c.Status(429)
		return
	}
	defer func() { <-a.passwordSlots }()
	if bcrypt.CompareHashAndPassword(hash, []byte(in.Current)) != nil {
		c.Status(403)
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	if err != nil {
		c.Status(503)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err = a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	var current []byte
	if a.db.QueryRow("SELECT password FROM users WHERE id=?", p.ID).Scan(&current) != nil || !bytes.Equal(current, hash) {
		c.Status(403)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		c.Status(503)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE users SET password=? WHERE id=?", newHash, p.ID); err == nil {
		_, err = tx.Exec("DELETE FROM sessions WHERE user=?", p.ID)
	}
	if err != nil || tx.Commit() != nil {
		c.Status(503)
		return
	}
	a.revokeLocked(p.ID, "", "")
	a.auditLocked(p, "password-change", p.ID)
	c.Status(200)
}

package server

import (
	"database/sql"
	"github.com/gin-gonic/gin"
	"time"
)

func migrateDeviceLifecycle(db *sql.DB) error {
	if err := migrateGroups(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 3 {
		_, err = tx.Exec(`ALTER TABLE devices ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN client_description TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN last_registered_at INTEGER NOT NULL DEFAULT 0;
PRAGMA user_version=3;`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Keep a tombstone and its original identity. Deletion never silently re-enables
// a credential or reuses an identity; all admission and mutation share mu.
func (a *TenantAuth) softDeleteDevice(c *gin.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.actorLocked(tenantSession(c))
	if err != nil {
		c.Status(401)
		return
	}
	if p.Tenant == "" || p.Role != "admin" {
		c.Status(403)
		return
	}
	id := c.Param("id")
	d, err := a.deviceLocked(id)
	if err != nil || d.Tenant != p.Tenant || d.DeletedAt != 0 {
		c.Status(404)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		c.Status(503)
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("UPDATE devices SET deleted_at=?,disabled=1,key_hash=? WHERE id=?", time.Now().Unix(), digest(randomID()), id)
	if err == nil {
		_, err = tx.Exec("DELETE FROM grants WHERE tenant=? AND device=?", p.Tenant, id)
	}
	if err == nil {
		_, err = tx.Exec("DELETE FROM device_groups WHERE tenant=? AND device=?", p.Tenant, id)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, "DELETE /api/manage/devices/:id", id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		c.Status(503)
		return
	}
	a.revokeLocked("", p.Tenant, id)
	if dev := a.srv.GetDevice(p.Tenant, id); dev != nil {
		go dev.Close(a.srv)
	}
	c.JSON(200, gin.H{"ok": true, "deleted": true})
}

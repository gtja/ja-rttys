package server

import (
	"database/sql"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// Group names are tenant-local keys. Renames update memberships and grants in
// one transaction. The legacy devices.grp column is never an authority again.
func migrateGroups(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 2 {
		_, err = tx.Exec(`
CREATE TABLE tenant_groups(tenant TEXT NOT NULL REFERENCES tenants(id), name TEXT NOT NULL, PRIMARY KEY(tenant,name));
CREATE UNIQUE INDEX device_tenant_id ON devices(tenant,id);
CREATE TABLE device_groups(
 tenant TEXT NOT NULL, device TEXT NOT NULL, grp TEXT NOT NULL,
 PRIMARY KEY(tenant,device,grp),
 FOREIGN KEY(tenant,device) REFERENCES devices(tenant,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant,grp) REFERENCES tenant_groups(tenant,name) ON UPDATE CASCADE ON DELETE CASCADE);
INSERT INTO tenant_groups SELECT tenant,grp FROM devices WHERE grp!='' UNION SELECT tenant,grp FROM grants WHERE device='' AND grp!='';
INSERT INTO device_groups SELECT tenant,id,grp FROM devices WHERE grp!='';
UPDATE devices SET grp='';
PRAGMA user_version=2;`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *TenantAuth) groupsLocked(tenant, device string) ([]string, error) {
	rows, err := a.db.Query("SELECT grp FROM device_groups WHERE tenant=? AND device=? ORDER BY grp", tenant, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		groups = append(groups, name)
	}
	return groups, rows.Err()
}

// Keep every still-authorized terminal/file/proxy lease. Admission and policy
// mutation share mu, so no new lease can slip between commit and revalidation.
func (a *TenantAuth) revalidateLocked(tenant string) {
	for id, lease := range a.live {
		if lease.Tenant != tenant {
			continue
		}
		p, err := a.actorLocked(lease.Session)
		d, deviceErr := a.deviceLocked(lease.Device)
		if err == nil && deviceErr == nil && lease.valid() && a.allowedLocked(p, d, true) {
			continue
		}
		lease.revoked.Store(true)
		delete(a.live, id)
		go lease.close()
	}
}

func (a *TenantAuth) manageGroups(c *gin.Context) {
	var in struct {
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	if c.Request.Method != "DELETE" && !tenantJSON(c, &in) {
		return
	}
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
	tx, err := a.db.Begin()
	if err != nil {
		c.Status(503)
		return
	}
	defer tx.Rollback()
	status := 200
	name := c.Param("name")
	resource := name
	if c.Request.Method == "PUT" {
		id := c.Param("id")
		resource = id
		var tenant string
		if tx.QueryRow("SELECT tenant FROM devices WHERE id=?", id).Scan(&tenant) != nil || tenant != p.Tenant {
			c.Status(404)
			return
		}
		if in.Groups == nil || len(in.Groups) > 128 {
			c.Status(400)
			return
		}
		for _, group := range in.Groups {
			var n int
			if tx.QueryRow("SELECT count(*) FROM tenant_groups WHERE tenant=? AND name=?", p.Tenant, group).Scan(&n) != nil || n != 1 {
				c.Status(404)
				return
			}
		}
		_, err = tx.Exec("DELETE FROM device_groups WHERE tenant=? AND device=?", p.Tenant, id)
		for _, group := range in.Groups {
			if err == nil {
				_, err = tx.Exec("INSERT OR IGNORE INTO device_groups(tenant,device,grp) VALUES(?,?,?)", p.Tenant, id, group)
			}
		}
	} else if c.Request.Method == "POST" {
		if !validName(in.Name) || strings.TrimSpace(in.Name) != in.Name {
			c.Status(400)
			return
		}
		resource = in.Name
		_, err = tx.Exec("INSERT INTO tenant_groups(tenant,name) VALUES(?,?)", p.Tenant, in.Name)
		status = 201
	} else {
		var n int
		if tx.QueryRow("SELECT count(*) FROM tenant_groups WHERE tenant=? AND name=?", p.Tenant, name).Scan(&n) != nil || n != 1 {
			c.Status(404)
			return
		}
		if c.Request.Method == "PATCH" {
			if !validName(in.Name) || strings.TrimSpace(in.Name) != in.Name {
				c.Status(400)
				return
			}
			_, err = tx.Exec("UPDATE tenant_groups SET name=? WHERE tenant=? AND name=?", in.Name, p.Tenant, name)
			if err == nil {
				_, err = tx.Exec("UPDATE grants SET grp=? WHERE tenant=? AND device='' AND grp=?", in.Name, p.Tenant, name)
			}
		} else {
			_, err = tx.Exec("DELETE FROM grants WHERE tenant=? AND device='' AND grp=?", p.Tenant, name)
			if err == nil {
				_, err = tx.Exec("DELETE FROM tenant_groups WHERE tenant=? AND name=?", p.Tenant, name)
			}
		}
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(unixepoch(),?,?,?,?,'ok')", p.ID, p.Tenant, c.Request.Method+" "+c.FullPath(), resource)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		c.JSON(409, gin.H{"error": "分组操作失败，请检查名称是否重复"})
		return
	}
	a.revalidateLocked(p.Tenant)
	c.JSON(status, gin.H{"ok": true, "name": in.Name})
}

func deviceHasGroup(d *TenantDevice, name string) bool {
	return slices.Contains(d.Groups, name)
}

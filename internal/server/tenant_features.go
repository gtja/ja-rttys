package server

import (
	"database/sql"
	"strings"

	"github.com/gin-gonic/gin"
)

type tenantFeatures struct {
	OTA   bool `json:"ota"`
	Tasks bool `json:"tasks"`
	Files bool `json:"files"`
	Web   bool `json:"web"`
}

func (a *TenantAuth) initTenantFeatures() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS tenant_features(
 tenant TEXT PRIMARY KEY REFERENCES tenants(id),ota INTEGER NOT NULL,tasks INTEGER NOT NULL,files INTEGER NOT NULL,web INTEGER NOT NULL);`)
	return err
}

// Unconfigured tenants retain their existing capabilities. These switches only
// narrow role/device grants; enabling a switch never supplies missing grants.
func (a *TenantAuth) featuresLocked(tenant string) (tenantFeatures, error) {
	if tenant == "" {
		return tenantFeatures{}, nil
	}
	f := tenantFeatures{}
	err := a.db.QueryRow("SELECT ota,tasks,files,web FROM tenant_features WHERE tenant=?", tenant).Scan(&f.OTA, &f.Tasks, &f.Files, &f.Web)
	if err == sql.ErrNoRows {
		return tenantFeatures{true, true, true, true}, nil
	}
	return f, err
}

func (a *TenantAuth) featureAllowedLocked(tenant, feature string) bool {
	if feature == "" {
		return true
	}
	f, err := a.featuresLocked(tenant)
	if err != nil {
		return false
	}
	switch feature {
	case "ota":
		return f.OTA
	case "tasks":
		return f.Tasks
	case "files":
		return f.Files
	case "web":
		return f.Web
	}
	return false
}

func (a *TenantAuth) featureAllowed(tenant, feature string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.featureAllowedLocked(tenant, feature)
}

func featureForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/ota"):
		return "ota"
	case path == "/api/tasks":
		return "tasks"
	case path == "/api/devices/:id/files" || path == "/api/devices/:id/file-transfer":
		return "files"
	case strings.HasPrefix(path, "/api/web"):
		return "web"
	}
	return ""
}

func (a *TenantAuth) otaReady(tenant string) bool {
	return a.ota != nil && a.ota.cfg.Prefixes[tenant] != ""
}

func (a *TenantAuth) manageTenantFeatures(c *gin.Context) {
	var in struct {
		Features map[string]bool `json:"features"`
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
	if !p.Admin {
		c.Status(403)
		return
	}
	if len(in.Features) != 4 {
		c.Status(400)
		return
	}
	for _, key := range []string{"ota", "tasks", "files", "web"} {
		if _, ok := in.Features[key]; !ok {
			c.Status(400)
			return
		}
	}
	id := c.Param("id")
	var count int
	if a.db.QueryRow("SELECT count(*) FROM tenants WHERE id=?", id).Scan(&count) != nil || count != 1 {
		c.Status(404)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		c.Status(503)
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO tenant_features(tenant,ota,tasks,files,web) VALUES(?,?,?,?,?) ON CONFLICT(tenant) DO UPDATE SET ota=excluded.ota,tasks=excluded.tasks,files=excluded.files,web=excluded.web", id, in.Features["ota"], in.Features["tasks"], in.Features["files"], in.Features["web"])
	if err == nil {
		_, err = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(unixepoch(),?,?,'tenant-features',?,'ok')", p.ID, id, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		c.Status(503)
		return
	}
	for key, lease := range a.live {
		if lease.Tenant == id && lease.Feature != "" && !in.Features[lease.Feature] {
			lease.revoked.Store(true)
			delete(a.live, key)
			go lease.close()
		}
	}
	c.JSON(200, gin.H{"features": in.Features, "ota_backend_ready": a.otaReady(id)})
}

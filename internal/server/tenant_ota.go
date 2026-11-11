package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type menderBinding struct {
	Tenant string `json:"tenant_id"`
	Device string `json:"mender_id"`
}
type menderSettings struct {
	ArtifactTool string                   `json:"artifact_tool"`
	SigningKeys  map[string]string        `json:"signing_keys"`
	URL          string                   `json:"url"`
	CA           string                   `json:"ca_file"`
	Username     string                   `json:"username"`
	Password     string                   `json:"password"`
	Prefixes     map[string]string        `json:"artifact_prefixes"`
	Bindings     map[string]menderBinding `json:"bindings"`
}
type menderBridge struct {
	cfg     menderSettings
	client  *http.Client
	mu      sync.Mutex
	token   string
	expires time.Time
}
type otaArtifact struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Types []string `json:"device_types_compatible"`
}
type otaJob struct {
	ID       string `json:"id"`
	Upstream string `json:"-"`
	Artifact string `json:"artifact"`
	Targets  string `json:"targets"`
	Created  int64  `json:"created_at"`
	State    string `json:"state"`
}

var otaID = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

func (a *TenantAuth) initOTA() error {
	if a.srv.cfg.MenderConfig == "" {
		return nil
	}
	b, err := os.ReadFile(a.srv.cfg.MenderConfig)
	if err != nil {
		return err
	}
	m := &menderBridge{}
	if err = json.Unmarshal(b, &m.cfg); err != nil {
		return err
	}
	u, err := url.Parse(m.cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return errors.New("Mender URL must be an HTTPS origin")
	}
	if m.cfg.Username == "" || m.cfg.Password == "" {
		return errors.New("Mender credentials missing")
	}
	for tenant, prefix := range m.cfg.Prefixes {
		if len(tenant) != 64 || prefix == "" {
			return errors.New("invalid OTA tenant prefix")
		}
		for other, p := range m.cfg.Prefixes {
			if other != tenant && (strings.HasPrefix(prefix, p) || strings.HasPrefix(p, prefix)) {
				return errors.New("OTA artifact prefixes overlap")
			}
		}
	}
	for id, b := range m.cfg.Bindings {
		if len(id) != 64 || !otaID.MatchString(b.Device) || m.cfg.Prefixes[b.Tenant] == "" {
			return errors.New("invalid OTA device binding")
		}
	}
	pem, err := os.ReadFile(m.cfg.CA)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return errors.New("invalid Mender CA")
	}
	m.client = &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, err = a.db.Exec(`CREATE TABLE IF NOT EXISTS ota_jobs(id TEXT NOT NULL,tenant TEXT NOT NULL REFERENCES tenants(id),upstream TEXT NOT NULL DEFAULT '',artifact TEXT NOT NULL,targets TEXT NOT NULL,created INTEGER NOT NULL,state TEXT NOT NULL,PRIMARY KEY(tenant,id));`)
	if err != nil {
		return err
	}
	if m.cfg.ArtifactTool != "" {
		if !filepath.IsAbs(m.cfg.ArtifactTool) {
			return errors.New("artifact_tool must be absolute")
		}
		for tenant, key := range m.cfg.SigningKeys {
			if m.cfg.Prefixes[tenant] == "" || !filepath.IsAbs(key) {
				return errors.New("invalid tenant signing key configuration")
			}
			if _, err = readPrivateSecretFile(key); err != nil {
				return err
			}
		}
	}
	_, err = a.db.Exec(`CREATE TABLE IF NOT EXISTS ota_versions(tenant TEXT NOT NULL,version TEXT NOT NULL,artifact TEXT NOT NULL UNIQUE,artifact_id TEXT NOT NULL DEFAULT '',image TEXT NOT NULL,binary_sha TEXT NOT NULL,model_sha TEXT NOT NULL,state TEXT NOT NULL,created INTEGER NOT NULL,PRIMARY KEY(tenant,version));UPDATE ota_versions SET state='interrupted' WHERE state='building';`)
	if err != nil {
		return err
	}
	a.ota = m
	return nil
}

func (m *menderBridge) login(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && time.Now().Before(m.expires) {
		return m.token, nil
	}
	req, err := http.NewRequestWithContext(ctx, "POST", m.cfg.URL+"/api/management/v1/useradm/auth/login", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(m.cfg.Username, m.cfg.Password)
	res, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("Mender authentication failed")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(b) > 65536 {
		return "", errors.New("invalid Mender token response")
	}
	m.token = strings.TrimSpace(string(b))
	m.expires = time.Now().Add(5 * time.Minute)
	return m.token, nil
}
func (m *menderBridge) request(ctx context.Context, method, path string, body any) ([]byte, http.Header, int, error) {
	token, err := m.login(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, m.cfg.URL+path, bytes.NewReader(data))
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := m.client.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024+1))
	if err != nil || len(b) > 4*1024*1024 {
		return nil, nil, 0, errors.New("Mender response exceeds limit")
	}
	if res.StatusCode == 401 {
		m.mu.Lock()
		m.token = ""
		m.mu.Unlock()
	}
	return b, res.Header, res.StatusCode, nil
}

func (a *TenantAuth) otaActor(c *gin.Context) (*Actor, *menderBridge) {
	p, err := a.actor(tenantSession(c))
	if err != nil {
		c.AbortWithStatus(401)
		return nil, nil
	}
	if p.Tenant == "" || p.Role != "admin" {
		c.AbortWithStatus(403)
		return nil, nil
	}
	if a.ota == nil || a.ota.cfg.Prefixes[p.Tenant] == "" {
		if c.FullPath() == "/api/ota" {
			c.JSON(200, gin.H{"enabled": false})
		} else {
			c.Status(503)
		}
		return nil, nil
	}
	return p, a.ota
}
func (a *TenantAuth) otaOverview(c *gin.Context) {
	p, m := a.otaActor(c)
	if p == nil {
		return
	}
	tenant := p.Tenant
	data, _, code, err := m.request(c.Request.Context(), "GET", "/api/management/v1/deployments/artifacts", nil)
	if err != nil || code != 200 {
		c.JSON(502, gin.H{"error": "Mender 版本列表暂不可用"})
		return
	}
	var all []otaArtifact
	if json.Unmarshal(data, &all) != nil {
		c.Status(502)
		return
	}
	artifacts := []otaArtifact{}
	for _, item := range all {
		if strings.HasPrefix(item.Name, m.cfg.Prefixes[p.Tenant]) {
			artifacts = append(artifacts, item)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err = a.actorLocked(p.Session)
	if err != nil {
		c.Status(401)
		return
	}
	if p.Tenant != tenant || p.Role != "admin" || !a.featureAllowedLocked(p.Tenant, "ota") {
		c.Status(403)
		return
	}
	devices := []gin.H{}
	for id, b := range m.cfg.Bindings {
		if b.Tenant == p.Tenant {
			d, e := a.deviceLocked(id)
			if e == nil && a.allowedLocked(p, d, true) {
				devices = append(devices, gin.H{"id": d.ID, "wire_id": d.WireID, "name": d.Name})
			}
		}
	}
	rows, err := a.db.Query("SELECT id,upstream,artifact,targets,created,state FROM ota_jobs WHERE tenant=? ORDER BY created DESC LIMIT 30", p.Tenant)
	if err != nil {
		c.Status(503)
		return
	}
	defer rows.Close()
	jobs := []otaJob{}
	for rows.Next() {
		var j otaJob
		if rows.Scan(&j.ID, &j.Upstream, &j.Artifact, &j.Targets, &j.Created, &j.State) != nil {
			c.Status(503)
			return
		}
		jobs = append(jobs, j)
	}
	if rows.Err() != nil {
		c.Status(503)
		return
	}
	versions := []otaVersion{}
	vr, e := a.db.Query("SELECT version,artifact,artifact_id,image,binary_sha,model_sha,state,created FROM ota_versions WHERE tenant=? AND state!='deleted' ORDER BY created DESC LIMIT 50", p.Tenant)
	if e != nil {
		c.Status(503)
		return
	}
	for vr.Next() {
		var v otaVersion
		if vr.Scan(&v.Version, &v.Artifact, &v.ArtifactID, &v.Image, &v.BinarySHA, &v.ModelSHA, &v.State, &v.Created) != nil {
			vr.Close()
			c.Status(503)
			return
		}
		versions = append(versions, v)
	}
	if vr.Err() != nil {
		vr.Close()
		c.Status(503)
		return
	}
	vr.Close()
	c.JSON(200, gin.H{"enabled": true, "can_import_artifact": m.cfg.ArtifactTool != "", "artifact_prefix": m.cfg.Prefixes[p.Tenant], "can_create_version": m.cfg.ArtifactTool != "" && m.cfg.SigningKeys[p.Tenant] != "", "versions": versions, "artifacts": artifacts, "devices": devices, "jobs": jobs})
}

func (a *TenantAuth) otaDeploy(c *gin.Context) {
	p, m := a.otaActor(c)
	if p == nil {
		return
	}
	tenant := p.Tenant
	var in struct {
		Request  string   `json:"request_id"`
		Artifact string   `json:"artifact_id"`
		Devices  []string `json:"device_ids"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	if !otaID.MatchString(in.Request) || !otaID.MatchString(in.Artifact) || len(in.Devices) == 0 || len(in.Devices) > 50 {
		c.Status(400)
		return
	}
	data, _, code, err := m.request(c.Request.Context(), "GET", "/api/management/v1/deployments/artifacts/"+url.PathEscape(in.Artifact), nil)
	var artifact otaArtifact
	if err != nil || code != 200 || json.Unmarshal(data, &artifact) != nil {
		c.Status(502)
		return
	}
	if !strings.HasPrefix(artifact.Name, m.cfg.Prefixes[p.Tenant]) {
		c.Status(404)
		return
	}
	sort.Strings(in.Devices)
	devices := []string{}
	seen := map[string]bool{}
	for _, id := range in.Devices {
		if !seen[id] {
			devices = append(devices, id)
			seen[id] = true
		}
	}
	targetJSON, _ := json.Marshal(devices)
	upstream := []string{}
	a.mu.Lock()
	p, err = a.actorLocked(p.Session)
	if err != nil || p.Role != "admin" || p.Tenant != tenant || !a.featureAllowedLocked(p.Tenant, "ota") {
		a.mu.Unlock()
		c.Status(403)
		return
	}
	for _, id := range devices {
		binding, ok := m.cfg.Bindings[id]
		d, e := a.deviceLocked(id)
		if !ok || binding.Tenant != p.Tenant || e != nil || !a.allowedLocked(p, d, true) {
			a.mu.Unlock()
			c.Status(404)
			return
		}
		upstream = append(upstream, binding.Device)
	}
	var oldArtifact, oldTargets, state string
	if a.db.QueryRow("SELECT artifact,targets,state FROM ota_jobs WHERE tenant=? AND id=?", p.Tenant, in.Request).Scan(&oldArtifact, &oldTargets, &state) == nil {
		a.mu.Unlock()
		if oldArtifact != artifact.Name || oldTargets != string(targetJSON) {
			c.Status(409)
			return
		}
		c.JSON(200, gin.H{"id": in.Request, "state": state})
		return
	}
	tx, e := a.db.Begin()
	if e == nil {
		_, e = tx.Exec("INSERT INTO ota_jobs(id,tenant,artifact,targets,created,state) VALUES(?,?,?,?,?,'creating')", in.Request, p.Tenant, artifact.Name, string(targetJSON), time.Now().Unix())
		if e == nil {
			_, e = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, "ota-deploy", in.Request)
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	a.mu.Unlock()
	if e != nil {
		c.Status(503)
		return
	}
	// Once admitted this is a durable deployment, independent of the browser's connection.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, headers, status, e := m.request(ctx, "POST", "/api/management/v1/deployments/deployments", gin.H{"name": "rttys-" + in.Request, "artifact_name": artifact.Name, "devices": upstream})
	uid := ""
	state = "unknown"
	if e == nil && status == 201 {
		u, parseErr := url.Parse(headers.Get("Location"))
		if parseErr == nil {
			parts := strings.Split(strings.TrimRight(u.Path, "/"), "/")
			uid = parts[len(parts)-1]
			if otaID.MatchString(uid) {
				state = "submitted"
			} else {
				uid = ""
			}
		}
	} else if e == nil && status >= 400 && status < 500 {
		state = "rejected"
	}
	a.mu.Lock()
	_, e = a.db.Exec("UPDATE ota_jobs SET upstream=?,state=? WHERE tenant=? AND id=?", uid, state, p.Tenant, in.Request)
	a.mu.Unlock()
	if e != nil {
		c.JSON(503, gin.H{"id": in.Request, "error": "任务状态保存失败，请勿重复发布"})
		return
	}
	c.JSON(200, gin.H{"id": in.Request, "state": state})
}
func (a *TenantAuth) otaJobStatus(c *gin.Context) {
	p, m := a.otaActor(c)
	if p == nil {
		return
	}
	id := c.Param("id")
	a.mu.Lock()
	var j otaJob
	err := a.db.QueryRow("SELECT id,upstream,artifact,targets,created,state FROM ota_jobs WHERE tenant=? AND id=?", p.Tenant, id).Scan(&j.ID, &j.Upstream, &j.Artifact, &j.Targets, &j.Created, &j.State)
	a.mu.Unlock()
	if err != nil {
		c.Status(404)
		return
	}
	if j.Upstream == "" {
		c.JSON(200, gin.H{"job": j})
		return
	}
	data, _, code, err := m.request(c.Request.Context(), "GET", "/api/management/v1/deployments/deployments/"+url.PathEscape(j.Upstream), nil)
	if err != nil || code != 200 {
		c.JSON(502, gin.H{"error": "升级状态暂不可用"})
		return
	}
	var status struct {
		Status string         `json:"status"`
		Stats  map[string]int `json:"stats"`
	}
	if json.Unmarshal(data, &status) != nil {
		c.Status(502)
		return
	}
	data, _, code, err = m.request(c.Request.Context(), "GET", "/api/management/v1/deployments/deployments/"+url.PathEscape(j.Upstream)+"/statistics", nil)
	if err != nil || code != 200 || json.Unmarshal(data, &status.Stats) != nil {
		c.JSON(502, gin.H{"error": "升级统计暂不可用"})
		return
	}
	c.JSON(200, gin.H{"job": j, "status": status.Status, "stats": status.Stats})
}

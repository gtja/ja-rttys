package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxImportedArtifact = 128 * 1024 * 1024
const menderArtifacts = "/api/management/v1/deployments/artifacts"

func (a *TenantAuth) otaArtifactWriteAllowed(p *Actor) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.actorLocked(p.Session)
	return err == nil && current.Tenant == p.Tenant && current.Role == "admin" && a.featureAllowedLocked(p.Tenant, "ota")
}

func (a *TenantAuth) otaImportArtifact(c *gin.Context) {
	p, m := a.otaActor(c)
	if p == nil {
		return
	}
	if m.cfg.ArtifactTool == "" {
		c.JSON(503, gin.H{"error": "尚未配置制品检查工具"})
		return
	}
	select {
	case versionBuildSlots <- struct{}{}:
		defer func() { <-versionBuildSlots }()
	default:
		c.JSON(429, gin.H{"error": "已有制品正在处理，请稍后重试"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportedArtifact+65536)
	form, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(400, gin.H{"error": "请选择 .mender 制品"})
		return
	}
	part, err := form.NextPart()
	if err != nil || part.FormName() != "artifact" || !strings.HasSuffix(strings.ToLower(part.FileName()), ".mender") {
		c.JSON(400, gin.H{"error": "请选择 .mender 制品"})
		return
	}
	file, err := os.CreateTemp("", "rttys-import-*.mender")
	if err != nil {
		c.Status(503)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(part, maxImportedArtifact+1))
	if size > maxImportedArtifact {
		c.JSON(413, gin.H{"error": "制品不得超过 128 MiB"})
		return
	}
	if err != nil || size == 0 {
		c.JSON(400, gin.H{"error": "读取制品失败或文件为空"})
		return
	}
	if _, err = form.NextPart(); err != io.EOF {
		c.JSON(400, gin.H{"error": "每次只能上传一个制品"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.cfg.ArtifactTool, "read", "--no-progress", file.Name())
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		c.Status(503)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(stdout, 65537))
	if err != nil || len(raw) > 65536 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		c.JSON(400, gin.H{"error": "制品检查输出超出限制"})
		return
	}
	rest, ok := strings.CutPrefix(strings.TrimLeft(string(raw), "\r\n"), "Mender Artifact:\n  Name: ")
	name, remainder, found := strings.Cut(rest, "\n")
	if cmd.Wait() != nil || !ok || !found || !strings.HasPrefix(remainder, "  Format: ") || len(name) > 256 || strings.TrimSpace(name) != name {
		c.JSON(400, gin.H{"error": "无法识别或校验 Mender 制品格式"})
		return
	}
	if !strings.HasPrefix(name, m.cfg.Prefixes[p.Tenant]) {
		c.JSON(403, gin.H{"error": "制品内部名称必须以当前租户的前缀开头"})
		return
	}
	if !a.otaArtifactWriteAllowed(p) {
		c.Status(403)
		return
	}
	token, err := m.login(ctx)
	if err != nil {
		c.JSON(502, gin.H{"error": "Mender 登录失败"})
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		c.Status(503)
		return
	}
	var framing bytes.Buffer
	writer := multipart.NewWriter(&framing)
	_, _ = writer.CreateFormFile("artifact", "release.mender")
	start := framing.Len()
	_ = writer.Close()
	body := io.MultiReader(bytes.NewReader(framing.Bytes()[:start]), file, bytes.NewReader(framing.Bytes()[start:]))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.URL+menderArtifacts, body)
	if err != nil {
		c.Status(503)
		return
	}
	req.ContentLength = int64(framing.Len()) + size
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res, err := m.client.Do(req)
	if err != nil {
		c.JSON(502, gin.H{"error": "上传结果未确认，请刷新版本库后检查"})
		return
	}
	defer res.Body.Close()
	id := path.Base(res.Header.Get("Location"))
	if res.StatusCode == 409 {
		c.JSON(409, gin.H{"error": "相同名称及兼容条件的制品已存在"})
		return
	}
	if res.StatusCode != 201 || !otaID.MatchString(id) {
		c.JSON(502, gin.H{"error": "Mender 未确认制品导入，请刷新版本库后检查"})
		return
	}
	a.mu.Lock()
	_, err = a.db.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, "ota-artifact-import", id)
	a.mu.Unlock()
	if err != nil {
		c.Error(err)
	}
	c.JSON(201, gin.H{"artifact_id": id, "artifact": name})
}

func (a *TenantAuth) otaDeleteArtifact(c *gin.Context) {
	p, m := a.otaActor(c)
	if p == nil {
		return
	}
	id := c.Param("id")
	var in struct {
		Name string `json:"name"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	if !otaID.MatchString(id) || in.Name == "" {
		c.Status(400)
		return
	}
	endpoint := menderArtifacts + "/" + id
	raw, _, status, err := m.request(c.Request.Context(), http.MethodGet, endpoint, nil)
	var artifact otaArtifact
	switch {
	case status == 404:
		c.Status(404)
		return
	case err != nil || status != 200 || json.Unmarshal(raw, &artifact) != nil || artifact.ID != id:
		c.Status(502)
		return
	case !strings.HasPrefix(artifact.Name, m.cfg.Prefixes[p.Tenant]):
		c.Status(404)
		return
	case artifact.Name != in.Name:
		c.JSON(409, gin.H{"error": "制品名称已变化，请刷新后重新确认"})
		return
	}
	if !a.otaArtifactWriteAllowed(p) {
		c.Status(403)
		return
	}
	_, _, status, err = m.request(c.Request.Context(), http.MethodDelete, endpoint, nil)
	switch {
	case status == 409:
		c.JSON(409, gin.H{"error": "制品正在被部署任务使用，请等任务结束后再删除"})
	case status == 404:
		c.Status(404)
	case err != nil || status != 204:
		c.JSON(502, gin.H{"error": "删除结果未确认，请刷新版本库后检查"})
	default:
		a.mu.Lock()
		tx, dbErr := a.db.Begin()
		if dbErr == nil {
			defer tx.Rollback()
			_, dbErr = tx.Exec("UPDATE ota_versions SET state='deleted' WHERE tenant=? AND artifact_id=?", p.Tenant, id)
			if dbErr == nil {
				_, dbErr = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, "ota-artifact-delete", id)
			}
			if dbErr == nil {
				dbErr = tx.Commit()
			}
		}
		a.mu.Unlock()
		if dbErr != nil {
			c.Error(dbErr)
			c.JSON(502, gin.H{"error": "制品已从 Mender 删除，但本地记录更新失败"})
			return
		}
		c.Status(204)
	}
}

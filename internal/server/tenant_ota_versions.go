package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
)

type otaVersion struct {
	Version    string `json:"version"`
	Artifact   string `json:"artifact"`
	ArtifactID string `json:"artifact_id"`
	Image      string `json:"image"`
	BinarySHA  string `json:"binary_sha256"`
	ModelSHA   string `json:"model_sha256"`
	State      string `json:"state"`
	Created    int64  `json:"created_at"`
}

var versionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)
var shaHex = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionBuildSlots = make(chan struct{}, 1)

func (a *TenantAuth) otaCreateVersion(c *gin.Context) {
	p, m := a.otaActor(c)
	if p == nil {
		return
	}
	key := m.cfg.SigningKeys[p.Tenant]
	if key == "" || m.cfg.ArtifactTool == "" {
		c.JSON(503, gin.H{"error": "当前租户尚未配置制品签名服务"})
		return
	}
	select {
	case versionBuildSlots <- struct{}{}:
		defer func() { <-versionBuildSlots }()
	default:
		c.JSON(429, gin.H{"error": "已有版本正在制作，请稍后重试"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*1024*1024)
	if err := c.Request.ParseMultipartForm(2 * 1024 * 1024); err != nil {
		c.JSON(400, gin.H{"error": "请提交版本信息和不超过 1 MiB 的 uavModel 文件"})
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	version := c.PostForm("version")
	image := c.PostForm("image")
	binary := c.PostForm("binary_sha256")
	if !versionName.MatchString(version) || len(image) != 71 || image[:7] != "sha256:" || !shaHex.MatchString(image[7:]) || !shaHex.MatchString(binary) {
		c.JSON(400, gin.H{"error": "版本号仅允许字母、数字、点、短横线及下划线；镜像 ID 与程序 SHA-256 需为完整摘要"})
		return
	}
	file, err := c.FormFile("model")
	if err != nil || file.Size <= 0 || file.Size > 1024*1024 {
		c.JSON(400, gin.H{"error": "请选择非空且不超过 1 MiB 的 uavModel 文件"})
		return
	}
	source, err := file.Open()
	if err != nil {
		c.Status(400)
		return
	}
	model, err := io.ReadAll(io.LimitReader(source, 1024*1024+1))
	source.Close()
	if err != nil || len(model) == 0 || len(model) > 1024*1024 {
		c.Status(400)
		return
	}
	var parsed map[string]json.RawMessage
	if json.Unmarshal(model, &parsed) != nil || len(parsed) == 0 {
		c.JSON(400, gin.H{"error": "uavModel 必须是有效的非空 JSON 对象"})
		return
	}
	sum := sha256.Sum256(model)
	modelSHA := hex.EncodeToString(sum[:])
	name := m.cfg.Prefixes[p.Tenant] + "psdk-" + version
	a.mu.Lock()
	actor, e := a.actorLocked(p.Session)
	if e != nil || actor.Tenant != p.Tenant || actor.Role != "admin" {
		a.mu.Unlock()
		c.Status(403)
		return
	}
	tx, e := a.db.Begin()
	if e == nil {
		_, e = tx.Exec("INSERT INTO ota_versions(tenant,version,artifact,image,binary_sha,model_sha,state,created) VALUES(?,?,?,?,?,?,'building',?)", p.Tenant, version, name, image, binary, modelSHA, time.Now().Unix())
		if e == nil {
			_, e = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, "ota-version-create", name)
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	a.mu.Unlock()
	if e != nil {
		c.JSON(409, gin.H{"error": "版本号已存在或无法保存。请刷新列表，不要重复提交同一个版本"})
		return
	}
	state := "failed"
	artifactID := ""
	defer func() {
		a.mu.Lock()
		_, err := a.db.Exec("UPDATE ota_versions SET state=?,artifact_id=? WHERE tenant=? AND version=?", state, artifactID, p.Tenant, version)
		a.mu.Unlock()
		if err != nil {
			c.Error(err)
		}
	}()
	temp, err := os.MkdirTemp("", "rttys-artifact-")
	if err != nil {
		c.Status(503)
		return
	}
	defer os.RemoveAll(temp)
	manifest, _ := json.Marshal(map[string]string{"image": image, "binary_sha256": binary, "model_sha256": modelSHA})
	if os.WriteFile(filepath.Join(temp, "manifest.json"), manifest, 0600) != nil || os.WriteFile(filepath.Join(temp, "uavModel"), model, 0600) != nil {
		c.Status(503)
		return
	}
	// Build the exact fixed module; no caller scripts, key paths or shell commands.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, m.cfg.ArtifactTool, "write", "module-image", "-T", "ja-psdk-container", "-n", name, "-c", "ja-nvidia-arm64-app", "-f", filepath.Join(temp, "manifest.json"), "-f", filepath.Join(temp, "uavModel"), "-k", key, "-o", filepath.Join(temp, "release.mender"))
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err = command.Run(); err != nil {
		c.JSON(502, gin.H{"error": "制品制作失败，请管理员检查签名配置与工具状态"})
		return
	}
	// Recheck current privileges before sending a newly signed release upstream.
	a.mu.Lock()
	actor, e = a.actorLocked(p.Session)
	a.mu.Unlock()
	if e != nil || actor.Tenant != p.Tenant || actor.Role != "admin" {
		c.Status(403)
		return
	}
	token, err := m.login(ctx)
	if err != nil {
		c.JSON(502, gin.H{"error": "Mender 登录失败"})
		return
	}
	content, err := os.ReadFile(filepath.Join(temp, "release.mender"))
	if err != nil || len(content) > 4*1024*1024 {
		c.Status(502)
		return
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("artifact", "release.mender")
	if err != nil {
		c.Status(503)
		return
	}
	part.Write(content)
	writer.Close()
	request, err := http.NewRequestWithContext(ctx, "POST", m.cfg.URL+"/api/management/v1/deployments/artifacts", &body)
	if err != nil {
		c.Status(503)
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	state = "unknown" // An interrupted upload may already have been accepted. Never auto-retry.
	response, err := m.client.Do(request)
	if err != nil {
		c.JSON(502, gin.H{"error": "导入结果未确认，请刷新列表或联系管理员，勿重复创建"})
		return
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 65536))
	response.Body.Close()
	if response.StatusCode != 201 {
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			state = "rejected"
		}
		c.JSON(502, gin.H{"error": fmt.Sprintf("Mender 导入未成功（%d），请查看版本记录", response.StatusCode)})
		return
	}
	artifactID = filepath.Base(response.Header.Get("Location"))
	if !otaID.MatchString(artifactID) {
		artifactID = ""
		c.JSON(502, gin.H{"error": "导入已接受，但版本编号未确认，请联系管理员"})
		return
	}
	state = "ready"
	c.JSON(201, gin.H{"version": version, "artifact": name, "artifact_id": artifactID, "model_sha256": modelSHA, "state": state})
}

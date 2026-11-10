package server

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhaojh329/rtty-go/proto"
)

//go:embed operations_agent.py
var operationsAgent string

//go:embed device_operations_defaults.sql
var deviceOperationsDefaultsSQL string

type operationProfile struct {
	Username string `json:"username"`
	Root     string `json:"root"`
}
type operationResult struct {
	Device string `json:"device_id"`
	Status string `json:"status"`
	Output any    `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

var operationSlots = make(chan struct{}, 8)
var taskSlots = make(chan struct{}, 2)
var operationLocks sync.Map
var osUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (a *TenantAuth) initOperations() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS device_operations(device TEXT PRIMARY KEY REFERENCES devices(id),username TEXT NOT NULL,root TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS operation_tasks(tenant TEXT NOT NULL,id TEXT NOT NULL,owner TEXT NOT NULL,template TEXT NOT NULL,targets TEXT NOT NULL,created INTEGER NOT NULL,state TEXT NOT NULL,results TEXT NOT NULL DEFAULT '[]',PRIMARY KEY(tenant,id));
 CREATE TABLE IF NOT EXISTS device_runtime(device TEXT PRIMARY KEY REFERENCES devices(id),attempt TEXT NOT NULL,attempted_at INTEGER NOT NULL,collected_at INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL,info TEXT NOT NULL DEFAULT '{}',error TEXT NOT NULL DEFAULT '');
 UPDATE device_runtime SET status='failed',error='服务重启，等待设备重新上线采集' WHERE status='collecting';
 UPDATE operation_tasks SET state='interrupted' WHERE state='running';`)
	if err == nil {
		_, err = a.db.Exec(deviceOperationsDefaultsSQL)
	}
	return err
}
func (a *TenantAuth) profileLocked(id string) (operationProfile, error) {
	var p operationProfile
	err := a.db.QueryRow("SELECT username,root FROM device_operations WHERE device=?", id).Scan(&p.Username, &p.Root)
	return p, err
}
func (a *TenantAuth) configureOperations(c *gin.Context) {
	var in operationProfile
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
	if !p.Admin || p.Tenant == "" {
		c.Status(403)
		return
	}
	d, err := a.deviceLocked(c.Param("id"))
	if err != nil || d.Tenant != p.Tenant || d.DeletedAt != 0 {
		c.Status(404)
		return
	}
	if !osUserPattern.MatchString(in.Username) || in.Username == "root" || !filepath.IsAbs(in.Root) || len(in.Root) > 1024 || strings.ContainsRune(in.Root, 0) {
		c.JSON(400, gin.H{"error": "请设置非 root 账号和允许访问的绝对目录"})
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		c.Status(503)
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO device_operations(device,username,root) VALUES(?,?,?) ON CONFLICT(device) DO UPDATE SET username=excluded.username,root=excluded.root", d.ID, in.Username, filepath.Clean(in.Root))
	if err == nil {
		_, err = tx.Exec("INSERT INTO audit(at,user,tenant,action,resource,result) VALUES(?,?,?,?,?,'ok')", time.Now().Unix(), p.ID, p.Tenant, "operations-configure", d.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		c.Status(503)
		return
	}
	a.revokeLocked("", p.Tenant, d.ID)
	c.JSON(200, in)
}
func (a *TenantAuth) deviceOverview(c *gin.Context) {
	p, d, err := a.access(tenantSession(c), c.Param("id"), false)
	if err != nil {
		if d == nil {
			c.Status(404)
		} else {
			c.Status(403)
		}
		return
	}
	a.mu.Lock()
	profile, _ := a.profileLocked(d.ID)
	runtime := a.runtimeLocked(d.ID)
	rows, err := a.db.Query("SELECT at,action,result FROM audit WHERE tenant=? AND resource=? ORDER BY at DESC LIMIT 20", p.Tenant, d.ID)
	events := []gin.H{}
	if err == nil {
		for rows.Next() {
			var at int64
			var action, result string
			if rows.Scan(&at, &action, &result) == nil {
				events = append(events, gin.H{"at": at, "action": action, "result": result})
			}
		}
		rows.Close()
	}
	a.mu.Unlock()
	live := gin.H{"online": false}
	if dev := a.srv.GetDeviceLink(d.Tenant, d.ID, c.Query("link")); dev != nil {
		live = gin.H{"online": true, "link": dev.link, "client_id": dev.wireID, "protocol": dev.proto, "source": dev.conn.RemoteAddr().String(), "connected_at": dev.timestamp, "connected": uint32(time.Now().Unix() - dev.timestamp), "uptime": dev.uptime.Load()}
	}
	c.JSON(200, gin.H{"device": d, "connection": live, "links": a.srv.linkInfo(d.Tenant, d.ID), "operations": profile, "events": events, "runtime": runtime})
}

func (dev *Device) runOperation(ctx context.Context, profile operationProfile, payload map[string]any) (json.RawMessage, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	token := randomID()
	ch := make(chan json.RawMessage, 1)
	req := &CommandReq{result: ch}
	dev.commands.Store(token, req)
	defer dev.commands.Delete(token)
	var msg strings.Builder
	for _, s := range []string{profile.Username, "python3", token} {
		msg.WriteString(s)
		msg.WriteByte(0)
	}
	msg.WriteByte(4)
	for _, s := range []string{"-c", operationsAgent, profile.Root, string(data)} {
		msg.WriteString(s)
		msg.WriteByte(0)
	}
	if msg.Len() > 65535 {
		return nil, errors.New("请求过大")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err = dev.WriteMsg(proto.MsgTypeCmd, []byte(msg.String())); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		return nil, errors.New("等待设备超时或访问已撤销；已发送操作可能仍在设备执行")
	case <-dev.ctx.Done():
		return nil, errors.New("设备已离线")
	}
}
func (a *TenantAuth) executeOperation(ctx context.Context, sid, id string, payload map[string]any, links ...string) (any, error) {
	p, d, err := a.access(sid, id, true)
	if err != nil {
		return nil, errors.New("无设备操作权限")
	}
	if _, loaded := operationLocks.LoadOrStore(id, true); loaded {
		return nil, errors.New("设备正在执行其他文件操作或巡检，请稍后重试")
	}
	defer operationLocks.Delete(id)
	select {
	case operationSlots <- struct{}{}:
		defer func() { <-operationSlots }()
	default:
		return nil, errors.New("操作并发已满，请稍后重试")
	}
	link := ""
	if len(links) > 0 {
		link = links[0]
	}
	dev := a.srv.GetDeviceLink(d.Tenant, id, link)
	if dev == nil {
		return nil, errors.New("设备离线")
	}
	ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
	defer cancel()
	feature := "files"
	if action := fmt.Sprint(payload["action"]); action == "disk" || action == "services" || action == "logs" {
		feature = "tasks"
	}
	lease, err := a.attachFeature(sid, id, feature, cancel)
	if err != nil {
		return nil, errors.New("访问已撤销")
	}
	defer a.detach(lease)
	a.mu.Lock()
	profile, err := a.profileLocked(id)
	a.mu.Unlock()
	if err != nil {
		return nil, errors.New("平台管理员尚未配置运行账号与文件目录")
	}
	a.mu.Lock()
	err = a.auditLocked(p, "operations-"+fmt.Sprint(payload["action"]), id)
	a.mu.Unlock()
	if err != nil {
		return nil, errors.New("审计写入失败")
	}
	raw, err := dev.runOperation(ctx, profile, payload)
	if err != nil {
		return nil, err
	}
	if _, _, err = a.access(sid, id, true); err != nil || lease.revoked.Load() {
		return nil, errors.New("访问已撤销")
	}
	var response struct {
		Err    int    `json:"err"`
		Code   int    `json:"code"`
		Msg    string `json:"msg"`
		Stdout string `json:"stdout"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil, errors.New("设备响应无效")
	}
	if response.Err != 0 {
		return nil, fmt.Errorf("客户端执行失败：%s", response.Msg)
	}
	output, err := base64.StdEncoding.DecodeString(response.Stdout)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if json.Unmarshal(output, &result) != nil {
		return nil, errors.New("设备缺少 Python 3 或执行输出无效")
	}
	if response.Code != 0 || result["error"] != nil {
		return nil, fmt.Errorf("%v", result["error"])
	}
	return result, nil
}
func (a *TenantAuth) deviceFiles(c *gin.Context) {
	p, d, err := a.access(tenantSession(c), c.Param("id"), true)
	if err != nil {
		if d == nil {
			c.Status(404)
		} else {
			c.Status(403)
		}
		return
	}
	var in struct {
		Action   string `json:"action"`
		Path     string `json:"path"`
		Transfer string `json:"transfer"`
		Data     string `json:"data"`
		Offset   int64  `json:"offset"`
		Size     int64  `json:"size"`
		Stamp    string `json:"stamp"`
		SHA      string `json:"sha256"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	switch in.Action {
	case "list", "read", "begin", "write", "finish", "cancel":
	default:
		c.Status(400)
		return
	}
	if len(in.Path) > 1024 || strings.ContainsRune(in.Path, 0) || in.Offset < 0 || in.Offset > 16*1024*1024 || in.Size < 0 || in.Size > 16*1024*1024 || len(in.Data) > 22000 {
		c.Status(400)
		return
	}
	if in.Action != "list" && in.Action != "read" && !otaID.MatchString(in.Transfer) {
		c.Status(400)
		return
	}
	payload := map[string]any{"action": in.Action, "path": in.Path, "offset": in.Offset, "size": in.Size, "stamp": in.Stamp, "sha256": in.SHA, "data": in.Data, "transfer": digest(p.ID + ":" + d.ID + ":" + in.Transfer)}
	result, err := a.executeOperation(c.Request.Context(), p.Session, d.ID, payload, c.Query("link"))
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, result)
}

func (a *TenantAuth) taskActor(c *gin.Context) *Actor {
	p, err := a.actor(tenantSession(c))
	if err != nil {
		c.Status(401)
		return nil
	}
	if p.Tenant == "" || p.Role != "admin" {
		c.Status(403)
		return nil
	}
	return p
}
func (a *TenantAuth) listTasks(c *gin.Context) {
	p := a.taskActor(c)
	if p == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rows, err := a.db.Query("SELECT id,template,targets,created,state,results FROM operation_tasks WHERE tenant=? ORDER BY created DESC LIMIT 30", p.Tenant)
	if err != nil {
		c.Status(503)
		return
	}
	defer rows.Close()
	tasks := []gin.H{}
	for rows.Next() {
		var id, t, targets, state, results string
		var created int64
		if rows.Scan(&id, &t, &targets, &created, &state, &results) != nil {
			c.Status(503)
			return
		}
		tasks = append(tasks, gin.H{"id": id, "template": t, "targets": json.RawMessage(targets), "created_at": created, "state": state, "results": json.RawMessage(results)})
	}
	c.JSON(200, tasks)
}
func (a *TenantAuth) createTask(c *gin.Context) {
	p := a.taskActor(c)
	if p == nil {
		return
	}
	var in struct {
		ID       string   `json:"id"`
		Template string   `json:"template"`
		Devices  []string `json:"device_ids"`
	}
	if !tenantJSON(c, &in) {
		return
	}
	if !otaID.MatchString(in.ID) || len(in.Devices) == 0 || len(in.Devices) > 20 || (in.Template != "disk" && in.Template != "services" && in.Template != "logs") {
		c.Status(400)
		return
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range in.Devices {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, _, err := a.access(p.Session, id, true); err != nil {
			c.Status(404)
			return
		}
		ids = append(ids, id)
	}
	targets, _ := json.Marshal(ids)
	select {
	case taskSlots <- struct{}{}:
	default:
		c.JSON(429, gin.H{"error": "已有两个批量任务执行中"})
		return
	}
	a.mu.Lock()
	_, err := a.db.Exec("INSERT INTO operation_tasks(tenant,id,owner,template,targets,created,state) VALUES(?,?,?,?,?,?,'running')", p.Tenant, in.ID, p.ID, in.Template, string(targets), time.Now().Unix())
	a.mu.Unlock()
	if err != nil {
		<-taskSlots
		c.JSON(409, gin.H{"error": "任务编号已存在或保存失败，请刷新任务列表"})
		return
	}
	go func() {
		defer func() { <-taskSlots }()
		results := []operationResult{}
		for _, id := range ids {
			// Recheck the role before every target; queued work does not inherit old privileges.
			actor, e := a.actor(p.Session)
			var output any
			if e != nil || actor.Tenant != p.Tenant || actor.Role != "admin" {
				e = errors.New("权限已撤销")
			} else {
				output, e = a.executeOperation(context.Background(), p.Session, id, map[string]any{"action": in.Template})
			}
			result := operationResult{Device: id, Status: "success", Output: output}
			if value, ok := output.(map[string]any); ok && e == nil {
				if code, ok := value["exit_code"].(float64); ok && code != 0 {
					e = fmt.Errorf("命令退出码 %d，请查看输出", int(code))
				}
			}
			if e != nil {
				result.Status = "failed"
				result.Error = e.Error()
			}
			results = append(results, result)
			encoded, _ := json.Marshal(results)
			a.mu.Lock()
			a.db.Exec("UPDATE operation_tasks SET results=? WHERE tenant=? AND id=?", string(encoded), p.Tenant, in.ID)
			a.mu.Unlock()
		}
		a.mu.Lock()
		a.db.Exec("UPDATE operation_tasks SET state='finished' WHERE tenant=? AND id=?", p.Tenant, in.ID)
		a.mu.Unlock()
	}()
	c.JSON(202, gin.H{"id": in.ID})
}

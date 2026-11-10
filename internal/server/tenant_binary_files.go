package server

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"hash"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/zhaojh329/rtty-go/proto"
	"github.com/zhaojh329/rttys/v5/internal/utils"
)

//go:embed binary_file_agent.py
var binaryFileAgent string

type binaryFilePeer struct {
	shellReady chan struct{}
	shellOnce  sync.Once
	probe      string
	*User
	mu                                  sync.Mutex
	direction, marker                   string
	expected                            int64
	checksum                            string
	digest                              hash.Hash
	transferred                         int64
	terminal                            []byte
	ready, eof, complete, uploadStarted bool
	credits                             int
	finished                            chan struct{}
	finishOnce                          sync.Once
}

func (p *binaryFilePeer) send(v any) {
	b, _ := json.Marshal(v)
	if p.WriteMsg(websocket.TextMessage, b) != nil {
		p.Close()
	}
}
func (p *binaryFilePeer) fail(message string) {
	p.finishOnce.Do(func() { p.send(gin.H{"type": "error", "error": message}); close(p.finished) })
}
func (p *binaryFilePeer) OnOpen(code byte) {
	if p.closed.Load() {
		p.dev.users.Delete(p.sid)
		p.dev.WriteMsg(proto.MsgTypeLogout, p.sid)
		return
	}
	p.pending <- code
}
func (p *binaryFilePeer) finishLocked() {
	if !p.ready || !p.complete || !p.eof {
		return
	}
	if p.transferred != p.expected || hex.EncodeToString(p.digest.Sum(nil)) != p.checksum {
		p.fail("文件长度或 SHA-256 不一致")
		return
	}
	p.finishOnce.Do(func() { p.send(gin.H{"type": "done", "size": p.transferred, "sha256": p.checksum}); close(p.finished) })
}
func (p *binaryFilePeer) OnData(data []byte) {
	if p.policy != nil && !p.policy.valid() {
		return
	}
	// Drain the hidden session; never expose terminal data or accept terminal input.
	p.dev.WriteMsg(proto.MsgTypeAck, p.sid, uint16(len(data)))
	p.mu.Lock()
	defer p.mu.Unlock()
	p.terminal = append(p.terminal, data...)
	for {
		index := bytes.IndexByte(p.terminal, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(string(p.terminal[:index]))
		p.terminal = p.terminal[index+1:]
		if line == p.probe {
			p.shellOnce.Do(func() { close(p.shellReady) })
			continue
		}
		if !strings.HasPrefix(line, p.marker) {
			continue
		}
		var msg struct {
			Type  string `json:"type"`
			Size  int64  `json:"size"`
			SHA   string `json:"sha256"`
			Error string `json:"error"`
			Name  string `json:"name"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, p.marker)), &msg) != nil {
			p.fail("设备传输响应无效")
			return
		}
		switch msg.Type {
		case "error":
			p.fail(msg.Error)
		case "ready":
			if p.ready || msg.Size < 0 || msg.Size > 16*1024*1024 {
				p.fail("设备文件大小无效")
				return
			}
			if p.direction == "download" {
				p.expected = msg.Size
				p.checksum = msg.SHA
			} else if p.expected != msg.Size {
				p.fail("设备上传大小不一致")
				return
			}
			p.ready = true
			p.send(gin.H{"type": "ready", "size": msg.Size, "name": downloadFilename(msg.Name), "sha256": p.checksum})
			p.finishLocked()
		case "complete":
			if msg.Size != p.expected || msg.SHA != p.checksum {
				p.fail("设备完成校验不一致")
				return
			}
			p.complete = true
			p.finishLocked()
		}
	}
	if len(p.terminal) > 16384 {
		p.terminal = p.terminal[len(p.terminal)-4096:]
	}
}
func (p *binaryFilePeer) OnFile(data []byte) {
	if len(data) == 0 || p.closed.Load() || (p.policy != nil && !p.policy.valid()) {
		return
	}
	if data[0] == proto.MsgTypeFileAbort {
		p.fail("设备取消或拒绝了传输")
		return
	}
	if p.direction == "download" {
		if data[0] != proto.MsgTypeFileSend && data[0] != proto.MsgTypeFileData {
			p.fail("无效下载消息")
			return
		}
		if data[0] == proto.MsgTypeFileData {
			p.mu.Lock()
			p.transferred += int64(len(data) - 1)
			p.digest.Write(data[1:])
			if len(data) == 1 {
				p.eof = true
			}
			tooLarge := p.transferred > 16*1024*1024
			p.mu.Unlock()
			if tooLarge {
				p.fail("下载超过大小限制")
				return
			}
		}
		p.User.OnFile(data) // Reuse the original binary download window and transfer IDs.
		p.mu.Lock()
		p.finishLocked()
		p.mu.Unlock()
	} else {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch data[0] {
		case proto.MsgTypeFileRecv:
			if p.uploadStarted {
				p.fail("重复上传请求")
				return
			}
			p.uploadStarted = true
			p.credits = 4
			if p.dev.WriteMsg(proto.MsgTypeFile, p.sid, proto.MsgTypeFileInfo, uint32(p.expected), "payload") != nil {
				p.fail("设备连接已断开")
				return
			}
			p.send(gin.H{"type": "uploadReady", "window": 4, "chunk_size": 32768})
		case proto.MsgTypeFileAck:
			if p.credits < 4 {
				p.credits++
			}
			p.send(gin.H{"type": "fileAck"})
		default:
			p.fail("无效上传消息")
		}
	}
}
func (p *binaryFilePeer) readBrowser() {
	for {
		typ, data, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		if p.policy == nil || !p.policy.valid() {
			return
		}
		if typ == websocket.BinaryMessage {
			if p.direction != "upload" || len(data) < 2 || len(data) > 32770 || data[0] != 1 || data[1] != proto.MsgTypeFileData {
				p.fail("仅允许文件数据，不能发送终端命令")
				return
			}
			p.mu.Lock()
			size := int64(len(data) - 2)
			if !p.uploadStarted || p.credits <= 0 || p.eof || p.transferred+size > p.expected || (size == 0 && p.expected != 0) {
				p.mu.Unlock()
				p.fail("上传窗口或长度无效")
				return
			}
			p.credits--
			p.transferred += size
			p.digest.Write(data[2:])
			if p.transferred == p.expected {
				p.eof = true
			}
			p.mu.Unlock()
			if p.dev.WriteMsg(proto.MsgTypeFile, p.sid, data[1:]) != nil {
				return
			}
		} else if typ == websocket.TextMessage {
			var msg UserMsg
			if json.Unmarshal(data, &msg) != nil {
				return
			}
			if msg.Type == "cancel" {
				p.dev.WriteMsg(proto.MsgTypeFile, p.sid, proto.MsgTypeFileAbort)
				p.dev.WriteMsg(proto.MsgTypeTermData, p.sid, []byte{3})
				p.fail("传输已取消")
				return
			}
			if p.direction != "download" || (msg.Type != "fileStart" && msg.Type != "fileAck") {
				p.fail("不允许的传输控制消息")
				return
			}
			if p.fileControl(&msg) != nil {
				return
			}
		} else {
			return
		}
	}
}

func (a *TenantAuth) binaryFiles(c *gin.Context) {
	actor, d, err := a.access(tenantSession(c), c.Param("id"), true)
	if err != nil {
		if d == nil {
			c.Status(404)
		} else {
			c.Status(403)
		}
		return
	}
	direction := c.Query("direction")
	path := c.Query("path")
	size, parseErr := strconv.ParseInt(c.Query("size"), 10, 64)
	checksum := c.Query("sha256")
	if (direction != "upload" && direction != "download") || path == "" || len(path) > 1024 || strings.ContainsRune(path, 0) {
		c.Status(400)
		return
	}
	if direction == "upload" {
		decoded, e := hex.DecodeString(checksum)
		if parseErr != nil || size < 0 || size > 16*1024*1024 || e != nil || len(decoded) != 32 {
			c.Status(400)
			return
		}
	}
	if _, loaded := operationLocks.LoadOrStore(d.ID, true); loaded {
		c.JSON(409, gin.H{"error": "设备有其他文件操作正在进行"})
		return
	}
	defer operationLocks.Delete(d.ID)
	select {
	case operationSlots <- struct{}{}:
		defer func() { <-operationSlots }()
	default:
		c.Status(429)
		return
	}
	dev := a.srv.GetDeviceLink(d.Tenant, d.ID, c.Query("link"))
	if dev == nil {
		c.JSON(409, gin.H{"error": "设备离线"})
		return
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	user := &User{conn: conn, sid: utils.GenUniqueID(), dev: dev, pending: make(chan byte, 1)}
	peer := &binaryFilePeer{User: user, direction: direction, marker: "RTTYFILE-" + randomID() + ":", expected: size, checksum: checksum, digest: sha256.New(), finished: make(chan struct{})}
	peer.shellReady = make(chan struct{})
	peer.probe = "RTTYREADY-" + randomID()
	defer peer.Close()
	lease, err := a.attachFeature(actor.Session, d.ID, "files", peer.Close)
	if err != nil {
		peer.fail("访问已撤销")
		return
	}
	user.policy = lease
	defer a.detach(lease)
	a.mu.Lock()
	profile, err := a.profileLocked(d.ID)
	if err == nil {
		err = a.auditLocked(actor, "binary-file-"+direction, d.ID)
	}
	a.mu.Unlock()
	if err != nil {
		peer.fail("运维账号未配置或审计失败")
		return
	}
	conn.SetReadLimit(64 * 1024)
	conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	dev.pending.Store(user.sid, peer)
	if err = dev.WriteMsg(proto.MsgTypeLogin, user.sid); err != nil {
		peer.fail("设备连接失败")
		return
	}
	select {
	case code := <-user.pending:
		if code != 0 {
			peer.fail("无法创建文件传输会话")
			return
		}
	case <-time.After(10 * time.Second):
		peer.fail("文件会话创建超时")
		return
	case <-dev.ctx.Done():
		return
	}
	if !lease.valid() {
		return
	}
	probeCommand := "printf '\\nRTTYREADY-%s\\n' '" + strings.TrimPrefix(peer.probe, "RTTYREADY-") + "'\n"
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	for probing := true; probing; {
		if err = dev.WriteMsg(proto.MsgTypeTermData, user.sid, []byte(probeCommand)); err != nil {
			return
		}
		select {
		case <-peer.shellReady:
			probing = false
		case <-ticker.C:
		case <-deadline.C:
			peer.fail("终端登录未就绪，请为客户端配置与运维账号一致的 username")
			return
		case <-dev.ctx.Done():
			return
		}
	}
	config, _ := json.Marshal(gin.H{"root": profile.Root, "username": profile.Username, "path": path, "direction": direction, "size": size, "sha256": checksum, "marker": peer.marker})
	script := "import base64,json\nCONFIG=json.loads(base64.b64decode('" + base64.StdEncoding.EncodeToString(config) + "'))\n" + binaryFileAgent
	delimiter := "RTTY_" + randomID()
	command := "stty -echo; python3 - <<'" + delimiter + "'\n" + script + "\n" + delimiter + "\n"
	if err = dev.WriteMsg(proto.MsgTypeTermData, user.sid, []byte(command)); err != nil {
		return
	}
	readDone := make(chan struct{})
	go func() { defer close(readDone); peer.readBrowser() }()
	select {
	case <-readDone:
	case <-peer.finished:
		// Let the browser drain queued binary frames and acknowledge completion by
		// closing. Closing TCP with unread window ACKs can reset the connection.
		select {
		case <-readDone:
		case <-time.After(5 * time.Second):
			conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "transfer finished"), time.Now().Add(time.Second))
		}
	case <-dev.ctx.Done():
	case <-time.After(5 * time.Minute):
		peer.fail("文件传输超时")
	}
	// Closing this dedicated terminal cleans up the helper and never touches other terminals.
	peer.Close()
	<-readDone
}

var _ devicePeer = (*binaryFilePeer)(nil)

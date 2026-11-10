package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zhaojh329/rtty-go/proto"
	"github.com/zhaojh329/rttys/v5/internal/utils"
)

const terminalBufferLimit = 128 * 1024

type mcpTerminal struct {
	m              *terminalMCP
	dev            *Device
	id, sid, owner string
	mu             sync.Mutex
	lease          *accessLease
	buffer         []byte
	base           int64
	last           time.Time
	closed         bool
	done           chan struct{}
	notify         chan struct{}
	pending        chan byte
}

func (p *mcpTerminal) OnOpen(code byte) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		if _, ok := p.dev.users.LoadAndDelete(p.sid); ok {
			p.dev.WriteMsg(proto.MsgTypeLogout, p.sid)
		}
		return
	}
	select {
	case p.pending <- code:
	default:
	}
}
func (p *mcpTerminal) OnData(data []byte) {
	p.mu.Lock()
	if p.closed || !p.lease.valid() {
		p.mu.Unlock()
		return
	}
	p.buffer = append(p.buffer, data...)
	if excess := len(p.buffer) - terminalBufferLimit; excess > 0 {
		p.buffer = append([]byte(nil), p.buffer[excess:]...)
		p.base += int64(excess)
	}
	p.mu.Unlock()
	select {
	case p.notify <- struct{}{}:
	default:
	}
	if len(data) > 0 {
		if p.dev.WriteMsg(proto.MsgTypeAck, p.sid, uint16(len(data))) != nil {
			p.Close()
		}
	}
}
func (p *mcpTerminal) OnFile(_ []byte) {
	p.dev.WriteMsg(proto.MsgTypeFile, p.sid, proto.MsgTypeFileAbort)
}
func (p *mcpTerminal) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.buffer = nil
	close(p.done)
	lease := p.lease
	p.dev.pending.Delete(p.sid)
	_, loaded := p.dev.users.LoadAndDelete(p.sid)
	p.mu.Unlock()
	p.m.termMu.Lock()
	delete(p.m.terminals, p.id)
	p.m.termMu.Unlock()
	if lease != nil {
		go p.m.a.detach(lease)
	}
	if loaded {
		p.dev.WriteMsg(proto.MsgTypeLogout, p.sid)
	}
}
func (p *mcpTerminal) watch() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-p.dev.ctx.Done():
			p.Close()
			return
		case <-ticker.C:
			p.mu.Lock()
			expired := !p.lease.valid() || time.Since(p.last) > 5*time.Minute
			p.mu.Unlock()
			if expired {
				p.Close()
				return
			}
		}
	}
}

type openTerminalInput struct {
	Device string `json:"device_id"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
}

func (m *terminalMCP) openTerminal(ctx context.Context, req *mcp.CallToolRequest, in openTerminalInput) (*mcp.CallToolResult, any, error) {
	actor, ti, err := m.credential(req, scopeTerminal)
	if err != nil {
		return nil, nil, errDenied
	}
	if in.Cols == 0 {
		in.Cols = 120
	}
	if in.Rows == 0 {
		in.Rows = 30
	}
	if in.Cols < 1 || in.Cols > 500 || in.Rows < 1 || in.Rows > 200 {
		return nil, nil, fmt.Errorf("invalid terminal size")
	}
	_, d, err := m.a.access(actor.Session, in.Device, true)
	if err != nil {
		return nil, nil, errDenied
	}
	dev := m.a.srv.GetDevice(d.Tenant, d.ID)
	if dev == nil {
		return nil, nil, fmt.Errorf("device offline")
	}
	p := &mcpTerminal{m: m, dev: dev, id: randomID(), sid: utils.GenUniqueID(), owner: ti.UserID, last: time.Now(), done: make(chan struct{}), notify: make(chan struct{}, 1), pending: make(chan byte, 1)}
	m.termMu.Lock()
	count := 0
	for _, existing := range m.terminals {
		if existing.owner == ti.UserID {
			count++
		}
	}
	if count >= 4 || len(m.terminals) >= 128 {
		m.termMu.Unlock()
		return nil, nil, fmt.Errorf("terminal limit reached")
	}
	m.terminals[p.id] = p
	m.termMu.Unlock()
	lease, err := m.a.attachUntil(actor.Session, d.ID, ti.Expiration.Unix(), p.Close)
	if err != nil {
		p.Close()
		return nil, nil, errDenied
	}
	p.mu.Lock()
	p.lease = lease
	closed := p.closed
	if !closed {
		dev.pending.Store(p.sid, p)
	}
	p.mu.Unlock()
	if closed {
		m.a.detach(lease)
		return nil, nil, errDenied
	}
	ok := false
	defer func() {
		if !ok {
			p.Close()
		}
	}()
	if dev.WriteMsg(proto.MsgTypeLogin, p.sid) != nil {
		return nil, nil, fmt.Errorf("device unavailable")
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case code := <-p.pending:
		if code != 0 {
			return nil, nil, fmt.Errorf("device busy")
		}
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-p.done:
		return nil, nil, errDenied
	case <-timer.C:
		return nil, nil, fmt.Errorf("terminal open timed out")
	case <-dev.ctx.Done():
		return nil, nil, fmt.Errorf("device disconnected")
	}
	if _, _, err = m.credential(req, scopeTerminal); err != nil || !lease.valid() {
		return nil, nil, errDenied
	}
	if dev.WriteMsg(proto.MsgTypeWinsize, p.sid, uint16(in.Cols), uint16(in.Rows)) != nil {
		return nil, nil, fmt.Errorf("device unavailable")
	}
	go p.watch()
	ok = true
	return nil, map[string]any{"session_id": p.id, "expires": lease.Expires, "next_cursor": 0, "idle_timeout_seconds": 300}, nil
}

type readTerminalInput struct {
	Session  string `json:"session_id"`
	Cursor   int64  `json:"cursor,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
	WaitMS   int    `json:"wait_ms,omitempty"`
}

func (m *terminalMCP) ownedTerminal(req *mcp.CallToolRequest, id string) (*mcpTerminal, *Actor, error) {
	actor, ti, err := m.credential(req, scopeTerminal)
	if err != nil {
		return nil, nil, errDenied
	}
	m.termMu.Lock()
	p := m.terminals[id]
	m.termMu.Unlock()
	if p == nil || p.owner != ti.UserID {
		return nil, nil, errDenied
	}
	if _, _, err = m.a.access(actor.Session, p.dev.id, true); err != nil {
		p.Close()
		return nil, nil, errDenied
	}
	p.mu.Lock()
	valid := !p.closed && p.lease.valid()
	p.mu.Unlock()
	if !valid {
		p.Close()
		return nil, nil, errDenied
	}
	return p, actor, nil
}
func (m *terminalMCP) readTerminal(ctx context.Context, req *mcp.CallToolRequest, in readTerminalInput) (*mcp.CallToolResult, any, error) {
	if in.MaxBytes == 0 {
		in.MaxBytes = 16384
	}
	if in.Cursor < 0 || in.MaxBytes < 1 || in.MaxBytes > 32768 || in.WaitMS < 0 || in.WaitMS > 10000 {
		return nil, nil, fmt.Errorf("invalid read bounds")
	}
	p, _, err := m.ownedTerminal(req, in.Session)
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	empty := in.Cursor == p.base+int64(len(p.buffer))
	p.mu.Unlock()
	if empty && in.WaitMS > 0 {
		timer := time.NewTimer(time.Duration(in.WaitMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-p.notify:
		case <-p.done:
			return nil, nil, errDenied
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	p, actor, err := m.ownedTerminal(req, in.Session)
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	if p.closed || !p.lease.valid() {
		p.mu.Unlock()
		return nil, nil, errDenied
	}
	end := p.base + int64(len(p.buffer))
	if in.Cursor > end {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("cursor exceeds available output")
	}
	truncated := in.Cursor < p.base
	cursor := max(in.Cursor, p.base)
	next := min(end, cursor+int64(in.MaxBytes))
	output := strings.ToValidUTF8(string(p.buffer[cursor-p.base:next-p.base]), "�")
	p.last = time.Now()
	p.mu.Unlock()
	if m.audit(actor, "mcp-terminal-read", p.dev.id, "ok") != nil {
		return nil, nil, fmt.Errorf("audit unavailable")
	}
	return nil, map[string]any{"output": output, "next_cursor": next, "truncated": truncated, "closed": false}, nil
}

type writeTerminalInput struct {
	Session string `json:"session_id"`
	Input   string `json:"input"`
}

func (m *terminalMCP) writeTerminal(_ context.Context, req *mcp.CallToolRequest, in writeTerminalInput) (*mcp.CallToolResult, any, error) {
	if len(in.Input) == 0 || len(in.Input) > 16384 {
		return nil, nil, fmt.Errorf("input must contain 1-16384 bytes")
	}
	p, actor, err := m.ownedTerminal(req, in.Session)
	if err != nil {
		return nil, nil, err
	}
	// Do not persist typed passwords, command text, or output in application logs.
	if m.audit(actor, "mcp-terminal-write", p.dev.id, fmt.Sprintf("bytes=%d", len(in.Input))) != nil {
		return nil, nil, fmt.Errorf("audit unavailable")
	}
	p.mu.Lock()
	valid := !p.closed && p.lease.valid()
	if valid {
		p.last = time.Now()
	}
	p.mu.Unlock()
	if !valid {
		return nil, nil, errDenied
	}
	if p.dev.WriteMsg(proto.MsgTypeTermData, p.sid, []byte(in.Input)) != nil {
		p.Close()
		return nil, nil, fmt.Errorf("device unavailable")
	}
	return nil, map[string]any{"accepted_bytes": len(in.Input)}, nil
}

type closeTerminalInput struct {
	Session string `json:"session_id"`
}

func (m *terminalMCP) closeTerminal(_ context.Context, req *mcp.CallToolRequest, in closeTerminalInput) (*mcp.CallToolResult, any, error) {
	p, actor, err := m.ownedTerminal(req, in.Session)
	if err != nil {
		return nil, nil, err
	}
	p.Close()
	if m.audit(actor, "mcp-terminal-close", p.dev.id, "ok") != nil {
		return nil, nil, fmt.Errorf("audit unavailable")
	}
	return nil, map[string]bool{"closed": true}, nil
}

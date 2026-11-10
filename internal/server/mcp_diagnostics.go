package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/valyala/bytebufferpool"
	"github.com/zhaojh329/rtty-go/proto"
	"github.com/zhaojh329/rttys/v5/internal/utils"
)

var serviceUnit = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,119}\.service$`)

type diagnosticInput struct {
	Device string `json:"device_id"`
	Check  string `json:"check"`
	Unit   string `json:"unit,omitempty"`
}

func diagnosticCommand(in diagnosticInput) (string, []string, error) {
	if in.Check != "service" && in.Check != "logs" && in.Unit != "" {
		return "", nil, fmt.Errorf("unit is only accepted by service and logs")
	}
	switch in.Check {
	case "identity":
		return "id", nil, nil
	case "uptime":
		return "uptime", nil, nil
	case "disk":
		return "df", []string{"-hP"}, nil
	case "memory":
		return "free", []string{"-m"}, nil
	case "processes":
		return "ps", []string{"-eo", "pid,ppid,user,stat,comm"}, nil
	case "service", "logs":
		if !serviceUnit.MatchString(in.Unit) {
			return "", nil, fmt.Errorf("a valid .service unit is required")
		}
		if in.Check == "service" {
			return "systemctl", []string{"is-active", "--", in.Unit}, nil
		}
		return "journalctl", []string{"--no-pager", "-n", "100", "-u", in.Unit, "-o", "short-iso"}, nil
	default:
		return "", nil, fmt.Errorf("unknown diagnostic check")
	}
}

type diagnosticResult struct {
	Code      int    `json:"code"`
	Error     int    `json:"err,omitempty"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

func (m *terminalMCP) diagnostic(ctx context.Context, req *mcp.CallToolRequest, in diagnosticInput) (*mcp.CallToolResult, any, error) {
	p, ti, err := m.credential(req, scopeDiagnostic)
	if err != nil {
		return nil, nil, errDenied
	}
	cmd, args, err := diagnosticCommand(in)
	if err != nil {
		return nil, nil, err
	}
	user := m.a.srv.cfg.MCPDiagnosticUser
	if user == "" {
		return nil, nil, fmt.Errorf("diagnostics are disabled: an administrator must configure mcp-diagnostic-user")
	}
	_, d, err := m.a.access(p.Session, in.Device, true)
	if err != nil {
		return nil, nil, errDenied
	}
	dev := m.a.srv.GetDevice(d.Tenant, d.ID)
	if dev == nil {
		return nil, nil, fmt.Errorf("device offline")
	}
	select {
	case m.diagnosticSlots <- struct{}{}:
		defer func() { <-m.diagnosticSlots }()
	default:
		return nil, nil, fmt.Errorf("diagnostic limit reached")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	lease, err := m.a.attachUntil(p.Session, d.ID, ti.Expiration.Unix(), cancel)
	if err != nil {
		return nil, nil, errDenied
	}
	defer m.a.detach(lease)
	if m.audit(p, "mcp-diagnostic-start", d.ID, in.Check+" "+in.Unit) != nil {
		return nil, nil, fmt.Errorf("audit unavailable")
	}
	// A differently named UID-0 account must not bypass the non-root policy.
	identity, err := dev.diagnosticExec(ctx, lease, user, "id", []string{"-u"})
	if err != nil {
		return nil, nil, err
	}
	uid, uidErr := strconv.ParseUint(strings.TrimSpace(identity.Stdout), 10, 32)
	if uidErr != nil || uid == 0 || identity.Code != 0 || identity.Error != 0 {
		return nil, nil, fmt.Errorf("diagnostic account must resolve to a non-root UID on the device")
	}
	result, err := dev.diagnosticExec(ctx, lease, user, cmd, args)
	if err != nil {
		return nil, nil, err
	}
	if _, _, err = m.credential(req, scopeDiagnostic); err != nil || !lease.valid() {
		return nil, nil, errDenied
	}
	if m.audit(p, "mcp-diagnostic-finish", d.ID, fmt.Sprintf("%s code=%d err=%d", in.Check, result.Code, result.Error)) != nil {
		return nil, nil, fmt.Errorf("audit unavailable")
	}
	return nil, result, nil
}

func (dev *Device) diagnosticExec(ctx context.Context, lease *accessLease, user, cmd string, args []string) (*diagnosticResult, error) {
	if !lease.valid() {
		return nil, errDenied
	}
	token := utils.GenUniqueID()
	result := make(chan json.RawMessage, 1)
	dev.commands.Store(token, &CommandReq{result: result})
	defer dev.commands.Delete(token)
	msg := bytebufferpool.Get()
	defer bytebufferpool.Put(msg)
	BpWriteCString(msg, user)
	BpWriteCString(msg, cmd)
	BpWriteCString(msg, token)
	msg.WriteByte(byte(len(args)))
	for _, arg := range args {
		BpWriteCString(msg, arg)
	}
	if err := dev.WriteMsg(proto.MsgTypeCmd, msg); err != nil {
		return nil, fmt.Errorf("device unavailable")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dev.ctx.Done():
		return nil, fmt.Errorf("device disconnected")
	case raw := <-result:
		if !lease.valid() || ctx.Err() != nil {
			return nil, errDenied
		}
		r := new(diagnosticResult)
		if json.Unmarshal(raw, r) != nil {
			return nil, fmt.Errorf("invalid device response")
		}
		for _, output := range []*string{&r.Stdout, &r.Stderr} {
			decoded, err := base64.StdEncoding.DecodeString(*output)
			if err != nil {
				return nil, fmt.Errorf("invalid device output")
			}
			if len(decoded) > 16384 {
				decoded = decoded[:16384]
				r.Truncated = true
			}
			*output = strings.ToValidUTF8(string(decoded), "�")
		}
		return r, nil
	}
}

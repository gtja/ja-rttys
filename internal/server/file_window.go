package server

import (
	"github.com/gorilla/websocket"
	jsoniter "github.com/json-iterator/go"
	"github.com/zhaojh329/rtty-go/proto"
)

const maxDownloadWindow = 16

type downloadFlow struct {
	id                                                  string
	active, started, modern, awaitingDevice, ignoreData bool
	outstanding, window                                 int
}

// Only one device-side ACK may be outstanding. Pipeline the higher-latency
// browser leg, not the legacy device protocol, so EOF/cancel cannot leave spare
// credits that accidentally start the next file.
func (u *User) nextFileBlockLocked() error {
	f := &u.download
	if !f.active || !f.started || f.awaitingDevice || f.outstanding >= f.window || u.closed.Load() {
		return nil
	}
	if u.policy != nil && !u.policy.valid() {
		return nil
	}
	f.awaitingDevice = true
	return u.dev.WriteMsg(proto.MsgTypeFile, u.sid, proto.MsgTypeFileAck)
}

func (u *User) fileControl(m *UserMsg) error {
	u.fileMu.Lock()
	defer u.fileMu.Unlock()
	f := &u.download
	if m.Type == "fileCanceled" {
		if m.TransferID != "" && (!f.active || m.TransferID != f.id) {
			return nil
		}
		if f.active {
			f.ignoreData = true
		}
		f.active = false
		return u.dev.WriteMsg(proto.MsgTypeFile, u.sid, proto.MsgTypeFileAbort)
	}
	if !f.active {
		if m.TransferID != "" || m.Type == "fileStart" {
			return nil
		}
		return u.dev.WriteMsg(proto.MsgTypeFile, u.sid, proto.MsgTypeFileAck)
	}
	if m.Type == "fileStart" {
		if m.TransferID != f.id || f.started {
			return nil
		}
		f.modern = true
		f.started = true
		f.window = max(1, min(maxDownloadWindow, m.Window))
	} else {
		if (f.modern && m.TransferID != f.id) || (!f.modern && m.TransferID != "") {
			return nil
		}
		if !f.started {
			f.started = true
		} else if f.outstanding > 0 {
			f.outstanding--
		} else {
			return nil
		}
	}
	return u.nextFileBlockLocked()
}

func (u *User) onFileWindow(data []byte) {
	u.fileMu.Lock()
	defer u.fileMu.Unlock()
	f := &u.download
	switch data[0] {
	case proto.MsgTypeFileSend:
		*f = downloadFlow{id: randomID(), active: true, window: 1}
		msg, _ := jsoniter.Marshal(map[string]any{"type": "sendfile", "name": string(data[1:]), "transfer_id": f.id, "max_window": maxDownloadWindow})
		if u.WriteMsg(websocket.TextMessage, msg) != nil {
			u.Close()
		}
	case proto.MsgTypeFileRecv:
		*f = downloadFlow{}
		u.WriteMsg(websocket.TextMessage, []byte(`{"type":"recvfile"}`))
	case proto.MsgTypeFileData:
		if f.ignoreData {
			return
		}
		if f.active {
			f.awaitingDevice = false
			if len(data) == 1 {
				f.active = false
				f.outstanding = 0
			} else {
				f.outstanding++
			}
		}
		if u.WriteMsg(websocket.BinaryMessage, append([]byte{1}, data[1:]...)) != nil {
			u.Close()
			return
		}
		if u.nextFileBlockLocked() != nil {
			u.Close()
		}
	case proto.MsgTypeFileAck:
		u.WriteMsg(websocket.TextMessage, []byte(`{"type":"fileAck"}`))
	case proto.MsgTypeFileAbort:
		f.active = false
		f.ignoreData = true
		if f.modern {
			msg, _ := jsoniter.Marshal(map[string]any{"type": "fileError", "transfer_id": f.id})
			u.WriteMsg(websocket.TextMessage, msg)
		} else {
			u.WriteMsg(websocket.BinaryMessage, []byte{1})
		}
	}
}

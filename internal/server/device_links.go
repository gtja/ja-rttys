package server

import (
	"github.com/gin-gonic/gin"
	"time"
)

// The token-selected internal ID and tenant remain physical-device identities. Link is only a
// transport selector; it never grants tenant membership or changes permissions.
func validDeviceLink(link string) bool {
	switch link {
	case "", "default", "5g", "14g", "test":
		return true
	}
	return false
}
func deviceConnectionKey(id, link string) string {
	if link == "" || link == "default" {
		return id
	}
	return id + "@" + link
}
func (srv *RttyServer) GetDeviceLink(group, id, link string) *Device {
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	g := srv.GetGroup(group, false)
	if g == nil {
		return nil
	}
	if link != "" {
		if !validDeviceLink(link) {
			return nil
		}
		if v, ok := g.devices.Load(deviceConnectionKey(id, link)); ok {
			return v.(*Device)
		}
		return nil
	}
	for _, name := range []string{"5g", "14g", "default", "test"} {
		if v, ok := g.devices.Load(deviceConnectionKey(id, name)); ok {
			return v.(*Device)
		}
	}
	return nil
}
func (srv *RttyServer) DeviceLinks(group, id string) []*Device {
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	result := []*Device{}
	g := srv.GetGroup(group, false)
	if g == nil {
		return result
	}
	for _, name := range []string{"5g", "14g", "default", "test"} {
		if v, ok := g.devices.Load(deviceConnectionKey(id, name)); ok {
			result = append(result, v.(*Device))
		}
	}
	return result
}
func (srv *RttyServer) linkInfo(group, id string) []gin.H {
	result := []gin.H{}
	for _, d := range srv.DeviceLinks(group, id) {
		name := d.link
		if name == "" {
			name = "default"
		}
		result = append(result, gin.H{"link": name, "client_id": d.wireID, "online": true, "connected": max(int64(0), time.Now().Unix()-d.timestamp), "connected_at": d.timestamp, "uptime": d.uptime.Load(), "source": d.conn.RemoteAddr().String(), "protocol": d.proto})
	}
	return result
}
func (srv *RttyServer) closeDeviceLinks(group, id string) {
	for _, d := range srv.DeviceLinks(group, id) {
		go d.Close(srv)
	}
}

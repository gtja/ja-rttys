package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Notifications contain no device data. Clients reload through the normal
// permission-checked APIs; slow readers coalesce changes instead of blocking
// device registration or accumulating an unbounded queue.
type deviceChangeHub struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]string
}

func (h *deviceChangeHub) subscribe(tenant string) (<-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers == nil {
		h.subscribers = make(map[chan struct{}]string)
	}
	ch := make(chan struct{}, 1)
	h.subscribers[ch] = tenant
	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		h.mu.Unlock()
	}
}

func (h *deviceChangeHub) publish(tenant string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, scope := range h.subscribers {
		if scope == tenant {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func (a *TenantAuth) deviceEvents(c *gin.Context) {
	p := principal(c)
	changes, unsubscribe := a.srv.deviceChanges.subscribe(p.Tenant)
	defer unsubscribe()
	valid := func() bool {
		current, err := a.actor(p.Session)
		return err == nil && current.Tenant == p.Tenant
	}
	if !valid() {
		c.Status(http.StatusUnauthorized)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	write := func(event string) bool {
		// Bound blocked writes without imposing a deadline on the whole stream.
		controller := http.NewResponseController(c.Writer)
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := fmt.Fprintf(c.Writer, "event: %s\ndata: {}\n\n", event)
		if err == nil {
			c.Writer.Flush()
		}
		_ = controller.SetWriteDeadline(time.Time{})
		return err == nil
	}
	// Every connection, including a reconnect, resynchronizes the current list.
	if _, err := fmt.Fprint(c.Writer, "retry: 3000\n\n"); err != nil || !write("devices-changed") {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-changes:
			if !valid() || !write("devices-changed") {
				return
			}
		case <-ticker.C:
			// A long-lived connection must not outlive its login or tenant scope.
			if !valid() || !write("heartbeat") {
				return
			}
		}
	}
}

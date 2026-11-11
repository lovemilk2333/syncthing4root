package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ── server-sent events hub ──────────────────────────────────────────────────
//
// The UI no longer polls. Every state change (process start/stop, network
// change, policy re-evaluation, config save, settings change) is pushed to
// connected browsers over a single SSE stream at /api/events.

type sseHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

var eventsHub = &sseHub{clients: make(map[chan []byte]struct{})}

func (h *sseHub) subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if _, ok := h.clients[ch]; ok {
			delete(h.clients, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

func (h *sseHub) broadcast(event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := []byte("event: " + event + "\ndata: " + string(data) + "\n\n")

	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// slow client: drop this frame, it will get the next snapshot
		}
	}
}

// ── snapshots ───────────────────────────────────────────────────────────────

type statusInfo struct {
	Running   bool   `json:"running"`
	PID       *int   `json:"pid"`
	URL       string `json:"url"`
	Username  string `json:"username"`
	Autostart bool   `json:"autostart"`
}

type netPolicyInfo struct {
	Config netPolicyConfig `json:"config"`
	State  netState        `json:"state"`
}

type eventSourceInfo struct {
	Netlink bool `json:"netlink"`
	Logcat  bool `json:"logcat"`
	Inotify bool `json:"inotify"`
}

type appSnapshot struct {
	Status       statusInfo      `json:"status"`
	NetPolicy    netPolicyInfo   `json:"netpolicy"`
	EventSources eventSourceInfo `json:"event_sources"`
}

func currentStatus() statusInfo {
	running := isRunning()
	info := statusInfo{
		Running:   running,
		URL:       getSyncthingURL(),
		Username:  authUser,
		Autostart: true,
	}
	if running {
		if pid, err := getSyncthingPID(); err == nil {
			info.PID = &pid
		}
	}
	if _, err := os.Stat(autostartFlag); err == nil {
		info.Autostart = false
	}
	return info
}

func buildSnapshot() appSnapshot {
	cfg := getNetPolicy()
	return appSnapshot{
		Status: currentStatus(),
		NetPolicy: netPolicyInfo{
			Config: cfg,
			State:  getLastNetState(cfg),
		},
		EventSources: eventSourceInfo{
			Netlink: netlinkHealthy.Load(),
			Logcat:  logcatHealthy.Load(),
			Inotify: inotifyHealthy.Load(),
		},
	}
}

// publishSnapshot pushes the complete current state to every connected UI.
// Building it is cheap because the network snapshot is cached by the monitor.
func publishSnapshot() {
	eventsHub.broadcast("snapshot", buildSnapshot())
}

// ── HTTP handler ────────────────────────────────────────────────────────────

func handleEvents(c *gin.Context) {
	ch, cancel := eventsHub.subscribe()
	defer cancel()

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	// send the current state immediately so the page renders without an
	// extra round trip
	if data, err := json.Marshal(buildSnapshot()); err == nil {
		fmt.Fprintf(c.Writer, "event: snapshot\ndata: %s\n\n", data)
	}
	c.Writer.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := c.Writer.Write(msg); err != nil {
				return
			}
			c.Writer.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

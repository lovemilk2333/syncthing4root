package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// ── retained-line config ────────────────────────────────────────────────────

const (
	defaultLogLines     = 128
	minLogLines         = 16
	maxLogLines         = 4000
	maxLogPartialBytes  = 64 * 1024
	logSubscriberBuffer = 1024
)

// logLineCfg is the server-side retained-line setting. It controls how many
// entries the in-memory log buffer keeps; nothing is written to disk.
var logLineCfg atomic.Int64

func init() { logLineCfg.Store(defaultLogLines) }

func logConfigPath() string {
	return filepath.Join(syncthingDir, ".logs_config")
}

// clampLogLines keeps a user-supplied value inside the supported range.
func clampLogLines(n int) int {
	if n < minLogLines {
		return minLogLines
	}
	if n > maxLogLines {
		return maxLogLines
	}
	return n
}

func logLimit() int { return int(logLineCfg.Load()) }

func loadLogsConfig() {
	raw, err := os.ReadFile(logConfigPath())
	if err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			logLineCfg.Store(int64(clampLogLines(n)))
		}
	}
	applyLogLimit(logLimit())
}

func saveLogsConfig(n int) error {
	n = clampLogLines(n)
	if err := os.WriteFile(logConfigPath(), []byte(strconv.Itoa(n)+"\n"), 0644); err != nil {
		return err
	}
	logLineCfg.Store(int64(n))
	applyLogLimit(n)
	return nil
}

// ── in-memory log buffer ────────────────────────────────────────────────────

// memoryLog keeps the most recent log lines in RAM. It implements io.Writer so
// it can be used directly as the stdout/stderr sink of the Syncthing process,
// and fans out new lines to any connected SSE clients.
type memoryLog struct {
	mu      sync.Mutex
	lines   []string
	partial []byte
	limit   int
	subs    map[chan string]struct{}
}

func newMemoryLog(limit int) *memoryLog {
	return &memoryLog{limit: limit, subs: map[chan string]struct{}{}}
}

// logSource is one selectable in-memory log stream.
type logSource struct {
	ID   string
	Name string
	log  *memoryLog
}

var (
	syncthingLog = newMemoryLog(defaultLogLines)
	webLog       = newMemoryLog(defaultLogLines)
	stateLog     = newMemoryLog(defaultLogLines)

	logSources = []*logSource{
		{ID: "state", Name: "Syncthing state changes", log: stateLog},
		{ID: "syncthing", Name: "Syncthing output", log: syncthingLog},
		{ID: "webserver", Name: "Web server", log: webLog},
	}
)

// recordState appends a timestamped Syncthing state transition to the
// selectable "state" log. The timestamp is ISO-8601 with an explicit numeric
// UTC offset, e.g. 2026-11-11T19:04:05+08:00 (never a bare "Z").
func recordState(action, reason string) {
	line := time.Now().Format("2006-01-02T15:04:05-07:00") + " " + action + " — " + reason
	_, _ = stateLog.Write([]byte(line + "\n"))
}

func findLogSource(id string) *logSource {
	for _, s := range logSources {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// applyLogLimit applies the retained-line setting to every in-memory source.
func applyLogLimit(n int) {
	for _, s := range logSources {
		s.log.setLimit(n)
	}
}

// captureProcessOutput tees this process's stdout/stderr into the in-memory
// "webserver" log while still forwarding to the original descriptors, so the
// management UI can show its own service output without writing a file.
func captureProcessOutput() {
	tee := func(orig **os.File) {
		r, w, err := os.Pipe()
		if err != nil {
			return
		}
		old := *orig
		*orig = w
		go func() {
			_, _ = io.Copy(io.MultiWriter(old, webLog), r)
		}()
	}
	tee(&os.Stdout)
	tee(&os.Stderr)
}

// setLimit changes how many lines are retained, discarding older entries.
func (m *memoryLog) setLimit(n int) {
	if n < minLogLines {
		n = minLogLines
	}
	if n > maxLogLines {
		n = maxLogLines
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.limit = n
	if len(m.lines) > n {
		m.lines = append([]string(nil), m.lines[len(m.lines)-n:]...)
	}
}

// appendLine assumes m.mu is held.
func (m *memoryLog) appendLine(line string) {
	m.lines = append(m.lines, line)
	if len(m.lines) > m.limit {
		drop := len(m.lines) - m.limit
		m.lines = append(m.lines[:0], m.lines[drop:]...)
	}

	for ch := range m.subs {
		select {
		case ch <- line:
		default:
			// The subscriber is too slow. Drop its oldest queued line instead
			// of blocking the Syncthing process on a browser.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- line:
			default:
			}
		}
	}
}

// Write splits p into complete lines. A trailing partial line is held until
// more data arrives, so long lines are not emitted twice or cut apart.
func (m *memoryLog) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := len(p)
	m.partial = append(m.partial, p...)
	for {
		i := bytes.IndexByte(m.partial, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(string(m.partial[:i]), "\r")
		m.appendLine(line)
		m.partial = m.partial[i+1:]
	}

	// Never let a writer that produces no newline grow memory without bound.
	if len(m.partial) >= maxLogPartialBytes {
		m.appendLine(strings.TrimSuffix(string(m.partial), "\r"))
		m.partial = m.partial[:0]
	}
	if cap(m.partial) > 4*len(m.partial) {
		m.partial = append([]byte(nil), m.partial...)
	}

	return n, nil
}

// flushPartial emits an unterminated final line (process exited).
func (m *memoryLog) flushPartial() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.partial) == 0 {
		return
	}
	m.appendLine(strings.TrimSuffix(string(m.partial), "\r"))
	m.partial = m.partial[:0]
}

// subscribe returns a snapshot of the retained lines plus a channel that
// receives every subsequent line. Both are taken atomically, so no line is
// lost or duplicated between history and live tail.
func (m *memoryLog) subscribe() ([]string, chan string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	history := append([]string(nil), m.lines...)
	ch := make(chan string, logSubscriberBuffer)
	m.subs[ch] = struct{}{}
	return history, ch
}

func (m *memoryLog) unsubscribe(ch chan string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, ch)
}

func (m *memoryLog) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.lines)
}

// clear drops all retained lines (used when the log source is restarted).
func (m *memoryLog) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = nil
	m.partial = nil
}

// ── HTTP handlers ───────────────────────────────────────────────────────────

func sendSSE(c *gin.Context, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

// logSourceInfo describes one selectable in-memory log stream.
type logSourceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

func handleLogsList(c *gin.Context) {
	now := time.Now().Unix()
	out := make([]logSourceInfo, 0, len(logSources))
	for _, s := range logSources {
		out = append(out, logSourceInfo{
			ID:       s.ID,
			Name:     s.Name,
			Size:     int64(s.log.len()),
			Modified: now,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"lines": logLimit(),
		"logs":  out,
	})
}

// handleLogsConfig updates the retained-line setting. Older entries are
// dropped from memory immediately.
func handleLogsConfig(c *gin.Context) {
	var req struct {
		Lines int `json:"lines"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Lines <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lines must be a positive number"})
		return
	}
	if err := saveLogsConfig(req.Lines); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save log config"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"lines": logLimit()})
}

// handleLogStream streams one in-memory source over SSE: first the retained
// history, then new lines as they are written.
func handleLogStream(c *gin.Context) {
	id := c.Query("source")
	if id == "" {
		id = "syncthing"
	}
	src := findLogSource(id)
	if src == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "log source not found"})
		return
	}

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	history, ch := src.log.subscribe()
	defer src.log.unsubscribe(ch)

	_ = sendSSE(c, "history", gin.H{"source": src.ID, "lines": history})
	_ = sendSSE(c, "ready", gin.H{"source": src.ID})

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case line := <-ch:
			if err := sendSSE(c, "line", line); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

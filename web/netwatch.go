package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ── event sources (no polling) ──────────────────────────────────────────────
//
// Two kernel-backed sources replace the old "run `ip`, `cmd wifi`, `settings`
// every N seconds" loop:
//
//   1. `ip monitor link address route` — a long-lived netlink listener. Any
//      route/address/link change (WiFi connect/disconnect, mobile data up/down,
//      VPN, tethering) wakes the policy monitor immediately.
//   2. `logcat -b events` — Android's own event log. WiFi state, connectivity,
//      battery and power-saver events are delivered here by the framework, so
//      we react to the same broadcasts the system UI does (no Frida/Xposed).
//   3. inotify on Android's settings database — battery saver (low_power) is
//      written to settings_global.xml, so a write event wakes the monitor.
//      inotify on the module directory catches external pidfile changes.
//
// There is deliberately no periodic battery/WiFi polling. The only timer that
// remains is the reachability probe (a network check cannot be event-driven).

var (
	netlinkHealthy  atomic.Bool
	logcatHealthy   atomic.Bool
	inotifyHealthy  atomic.Bool
	netEventCh      = make(chan struct{}, 1)
	settingsEventCh = make(chan struct{}, 1)
	statusEventCh   = make(chan struct{}, 1)
)

// signal sends a non-blocking wake-up; duplicate events are coalesced.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// startEventSources launches the kernel and Android event watchers.
func startEventSources() {
	go netlinkLoop()
	go logcatLoop()
	go inotifyLoop()
}

// ── debouncer ───────────────────────────────────────────────────────────────

type debouncer struct {
	mu    sync.Mutex
	delay time.Duration
	fn    func()
	timer *time.Timer
}

func newDebouncer(d time.Duration, fn func()) *debouncer {
	return &debouncer{delay: d, fn: fn}
}

func (d *debouncer) touch() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, d.fn)
}

// ── netlink (`ip monitor`) ──────────────────────────────────────────────────

func netlinkLoop() {
	debounce := newDebouncer(350*time.Millisecond, func() { signal(netEventCh) })

	for {
		started := time.Now()
		cmd := exec.Command("ip", "monitor", "link", "address", "route")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			netlinkHealthy.Store(false)
			time.Sleep(10 * time.Second)
			continue
		}
		if err := cmd.Start(); err != nil {
			netlinkHealthy.Store(false)
			time.Sleep(10 * time.Second)
			continue
		}

		netlinkHealthy.Store(true)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 64*1024)
		for scanner.Scan() {
			// any line is a real kernel event; coalesce bursts
			debounce.touch()
		}
		_ = cmd.Wait()

		netlinkHealthy.Store(false)
		// `ip monitor` exits when the kernel socket is lost. If it dies right
		// away the command is probably unsupported; back off instead of
		// respawning in a tight loop.
		if time.Since(started) < 5*time.Second {
			time.Sleep(30 * time.Second)
		} else {
			time.Sleep(3 * time.Second)
		}
	}
}

// ── Android event log (`logcat -b events`) ──────────────────────────────────

// androidEventKeywords selects framework events that can change the policy
// inputs. Matching loosely is fine: a match only triggers one status read, it
// does not itself decide anything.
var androidEventKeywords = []string{
	"wifi", "network", "connectivity", "battery", "power_saver", "low_power",
	"data_conn", "data_state", "mobile", "telephony", "default_network",
}

func isRelevantAndroidEvent(line string) bool {
	l := strings.ToLower(line)
	for _, k := range androidEventKeywords {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

func logcatLoop() {
	debounce := newDebouncer(400*time.Millisecond, func() { signal(netEventCh) })

	for {
		started := time.Now()
		bin := "logcat"
		if _, err := exec.LookPath(bin); err != nil {
			bin = "/system/bin/logcat"
		}

		cmd := exec.Command(bin, "-b", "events", "-v", "raw")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			logcatHealthy.Store(false)
			time.Sleep(15 * time.Second)
			continue
		}
		if err := cmd.Start(); err != nil {
			logcatHealthy.Store(false)
			time.Sleep(15 * time.Second)
			continue
		}

		logcatHealthy.Store(true)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 256*1024)
		for scanner.Scan() {
			if isRelevantAndroidEvent(scanner.Text()) {
				debounce.touch()
			}
		}
		_ = cmd.Wait()

		logcatHealthy.Store(false)
		if time.Since(started) < 5*time.Second {
			time.Sleep(30 * time.Second)
		} else {
			time.Sleep(3 * time.Second)
		}
	}
}

// ── inotify (settings database + pidfile) ───────────────────────────────────

type watchKind int

const (
	watchSettings watchKind = iota
	watchModule
)

func inotifyLoop() {
	for {
		if !runInotify() {
			inotifyHealthy.Store(false)
			time.Sleep(15 * time.Second)
		}
	}
}

// runInotify blocks until the inotify fd fails. It returns true only when it
// successfully ran for a while (so the caller can decide whether to retry).
func runInotify() bool {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return false
	}
	defer unix.Close(fd)

	kinds := make(map[int]watchKind)
	add := func(path string, kind watchKind) {
		wd, err := unix.InotifyAddWatch(fd, path, unix.IN_CLOSE_WRITE|unix.IN_MOVED_TO|unix.IN_CREATE|unix.IN_DELETE|unix.IN_MODIFY)
		if err == nil {
			kinds[wd] = kind
		}
	}

	// Settings.Global is stored per user. Watch each user dir plus the
	// device-encrypted location.
	if entries, err := os.ReadDir("/data/system/users"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add(filepath.Join("/data/system/users", e.Name()), watchSettings)
			}
		}
	}
	add("/data/system/users/0", watchSettings)
	add("/data/user_de/0", watchSettings)
	add(syncthingDir, watchModule)

	if len(kinds) == 0 {
		return false
	}

	settingsDebounce := newDebouncer(400*time.Millisecond, func() { signal(settingsEventCh) })
	moduleDebounce := newDebouncer(200*time.Millisecond, func() { signal(statusEventCh) })
	inotifyHealthy.Store(true)

	buf := make([]byte, 64*1024)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			inotifyHealthy.Store(false)
			return true
		}
		if n < unix.SizeofInotifyEvent {
			continue
		}

		offset := 0
		for offset+unix.SizeofInotifyEvent <= n {
			raw := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			nameLen := int(raw.Len)
			next := offset + unix.SizeofInotifyEvent + nameLen
			if next > n {
				break
			}

			name := ""
			if nameLen > 0 {
				b := buf[offset+unix.SizeofInotifyEvent : next]
				if i := bytes.IndexByte(b, 0); i >= 0 {
					b = b[:i]
				}
				name = string(b)
			}

			switch kinds[int(raw.Wd)] {
			case watchSettings:
				// low_power / battery saver lives in settings_global.xml.
				// Some ROMs mirror settings into secure/device_protected.
				if strings.HasPrefix(name, "settings_global") ||
					strings.HasPrefix(name, "settings_secure") {
					settingsDebounce.touch()
				}
			case watchModule:
				// pidfile created/removed means Syncthing started/stopped
				// outside the web UI (boot service, manual kill, ...).
				if name == "syncthing.pid" || name == "" {
					moduleDebounce.touch()
				}
			}

			offset = next
		}
	}
}

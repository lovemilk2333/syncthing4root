package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ── config types ──────────────────────────────────────────────────────────

type wifiPolicy struct {
	Enabled   bool     `json:"enabled"`
	Whitelist []string `json:"whitelist"`
}

type cellularPolicy struct {
	Enabled bool `json:"enabled"` // true = treat cellular as "should stop"
}

type powerPolicy struct {
	Enabled bool `json:"enabled"` // true = stop when system battery saver is on
}

type probePolicy struct {
	Enabled       bool   `json:"enabled"`
	Type          string `json:"type"`   // "ping" | "tcp"
	Target        string `json:"target"` // host or IP
	Port          int    `json:"port"`   // used for tcp
	TimeoutMS     int    `json:"timeout_ms"`
	UpThreshold   int    `json:"up_threshold"`   // consecutive successes to go up
	DownThreshold int    `json:"down_threshold"` // consecutive failures to go down
}

// knownTerms are the identifiers usable in the policy expression.
var knownTerms = []string{"wifi", "cellular", "power", "probe"}

type netPolicyConfig struct {
	Enabled     bool           `json:"enabled"`
	Expr        string         `json:"expr"` // boolean DSL over knownTerms
	IntervalSec int            `json:"interval_sec"`
	WiFi        wifiPolicy     `json:"wifi"`
	Cellular    cellularPolicy `json:"cellular"`
	Power       powerPolicy    `json:"power"`
	Probe       probePolicy    `json:"probe"`
}

func defaultNetPolicy() netPolicyConfig {
	return netPolicyConfig{
		Enabled:     false,
		Expr:        "",
		IntervalSec: 30,
		WiFi:        wifiPolicy{Enabled: false, Whitelist: []string{}},
		Cellular:    cellularPolicy{Enabled: false},
		Power:       powerPolicy{Enabled: false},
		Probe: probePolicy{
			Enabled: false, Type: "ping", Target: "", Port: 22,
			TimeoutMS: 2000, UpThreshold: 2, DownThreshold: 3,
		},
	}
}

// ── runtime state ─────────────────────────────────────────────────────────

var (
	npMu     sync.RWMutex    // guards npCfg
	npCfg    netPolicyConfig // current policy config
	npWakeCh = make(chan struct{}, 1)

	// probe hysteresis counters (monitor goroutine only, no lock needed)
	probeUpCount   int
	probeDownCount int
	probeState     bool // last committed probe up/down state
)

func netPolicyPath() string {
	return filepath.Join(syncthingDir, ".netpolicy.json")
}

func getNetPolicy() netPolicyConfig {
	npMu.RLock()
	defer npMu.RUnlock()
	return npCfg
}

func loadNetPolicy() {
	npMu.Lock()
	defer npMu.Unlock()
	npCfg = defaultNetPolicy()
	data, err := os.ReadFile(netPolicyPath())
	if err != nil {
		return
	}
	var c netPolicyConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return
	}
	// migrate legacy configs (pre-expression) that used a "combine" field
	if strings.TrimSpace(c.Expr) == "" {
		c.Expr = migrateLegacyExpr(data, c)
	}
	npCfg = normalizeNetPolicy(c)
}

// migrateLegacyExpr builds an equivalent expression from an old-style config
// (combine + per-condition enabled). Returns "" if nothing was enabled.
func migrateLegacyExpr(raw []byte, c netPolicyConfig) string {
	var legacy struct {
		Combine string `json:"combine"`
	}
	_ = json.Unmarshal(raw, &legacy)
	op := " AND "
	if strings.ToUpper(legacy.Combine) == "OR" {
		op = " OR "
	}
	var terms []string
	if c.WiFi.Enabled {
		terms = append(terms, "wifi")
	}
	if c.Cellular.Enabled {
		terms = append(terms, "NOT cellular")
	}
	if c.Power.Enabled {
		terms = append(terms, "NOT power")
	}
	if c.Probe.Enabled {
		terms = append(terms, "probe")
	}
	return strings.Join(terms, op)
}

func saveNetPolicy(c netPolicyConfig) error {
	if err := validateExpr(c.Expr, knownTerms); err != nil {
		return fmt.Errorf("invalid expression: %w", err)
	}
	c = normalizeNetPolicy(c)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(netPolicyPath(), data, 0644); err != nil {
		return err
	}
	npMu.Lock()
	npCfg = c
	npMu.Unlock()
	// wake the monitor to re-evaluate immediately
	select {
	case npWakeCh <- struct{}{}:
	default:
	}
	return nil
}

// normalizeNetPolicy clamps/repairs values so a hand-edited or partial config
// can't break the monitor.
func normalizeNetPolicy(c netPolicyConfig) netPolicyConfig {
	c.Expr = strings.TrimSpace(c.Expr)
	if c.IntervalSec < 5 {
		c.IntervalSec = 5
	}
	if c.IntervalSec > 3600 {
		c.IntervalSec = 3600
	}
	if c.WiFi.Whitelist == nil {
		c.WiFi.Whitelist = []string{}
	}
	if c.Probe.Type != "tcp" {
		c.Probe.Type = "ping"
	}
	if c.Probe.Port <= 0 || c.Probe.Port > 65535 {
		c.Probe.Port = 22
	}
	if c.Probe.TimeoutMS < 200 {
		c.Probe.TimeoutMS = 200
	}
	if c.Probe.TimeoutMS > 60000 {
		c.Probe.TimeoutMS = 60000
	}
	if c.Probe.UpThreshold < 1 {
		c.Probe.UpThreshold = 1
	}
	if c.Probe.DownThreshold < 1 {
		c.Probe.DownThreshold = 1
	}
	return c
}

// ── network detection (root, via Android system commands) ───────────────────

// transport reports the active default-route transport: "wifi", "cellular",
// "other", or "none".
func detectTransport() string {
	out, err := exec.Command("ip", "route", "get", "8.8.8.8").CombinedOutput()
	if err != nil {
		return "none"
	}
	// e.g. "8.8.8.8 via 192.168.1.1 dev wlan0 src 192.168.1.100 uid 0"
	fields := strings.Fields(string(out))
	dev := ""
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			dev = fields[i+1]
			break
		}
	}
	return ifaceTransport(dev)
}

func ifaceTransport(dev string) string {
	switch {
	case dev == "":
		return "none"
	case strings.HasPrefix(dev, "wlan"), strings.HasPrefix(dev, "ap"):
		return "wifi"
	case strings.HasPrefix(dev, "rmnet"), strings.HasPrefix(dev, "ccmni"),
		strings.HasPrefix(dev, "rmnet_data"), strings.HasPrefix(dev, "pdp"),
		strings.HasPrefix(dev, "clat"):
		return "cellular"
	default:
		return "other"
	}
}

// detectSSID returns the currently-connected WiFi SSID, or "" if unknown.
// Tries several commands because output varies a lot across Android versions.
func detectSSID() string {
	// Android 10+: `cmd wifi status` → 'Wifi is connected to "SSID"'
	if out, err := exec.Command("cmd", "wifi", "status").CombinedOutput(); err == nil {
		if s := parseSSIDFromQuoted(string(out)); s != "" {
			return s
		}
	}
	// fallback: dumpsys wifi, look for 'SSID: <name>,' in mWifiInfo
	if out, err := exec.Command("dumpsys", "wifi").CombinedOutput(); err == nil {
		if s := parseSSIDFromDumpsys(string(out)); s != "" {
			return s
		}
	}
	return ""
}

// parseSSIDFromQuoted extracts the first double-quoted SSID from text like
// 'Wifi is connected to "MyNet"'.
func parseSSIDFromQuoted(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if !strings.Contains(line, "connected to") {
			continue
		}
		if a := strings.Index(line, `"`); a >= 0 {
			rest := line[a+1:]
			if b := strings.Index(rest, `"`); b >= 0 {
				return rest[:b]
			}
		}
	}
	return ""
}

// parseSSIDFromDumpsys extracts SSID from a 'SSID: <name>,' field.
func parseSSIDFromDumpsys(s string) string {
	idx := strings.Index(s, "SSID:")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(s[idx+len("SSID:"):])
	// value ends at comma or newline
	end := len(rest)
	if c := strings.IndexAny(rest, ",\n"); c >= 0 {
		end = c
	}
	ssid := strings.TrimSpace(rest[:end])
	ssid = strings.Trim(ssid, `"`)
	if ssid == "" || ssid == "<unknown ssid>" || ssid == "<none>" {
		return ""
	}
	return ssid
}

// detectPowerSave reports whether the system battery-saver (low-power) mode is
// currently on. Falls back across a couple of sources since ROMs vary.
func detectPowerSave() bool {
	// primary: global setting flipped by Battery Saver (1 = on)
	if out, err := exec.Command("settings", "get", "global", "low_power").CombinedOutput(); err == nil {
		if parsePowerSaveFlag(string(out)) {
			return true
		}
	}
	// fallback: dumpsys power exposes mSettingBatterySaverEnabled / mLowPowerModeEnabled
	if out, err := exec.Command("dumpsys", "power").CombinedOutput(); err == nil {
		if parseDumpsysPowerSave(string(out)) {
			return true
		}
	}
	return false
}

// parsePowerSaveFlag reads the `settings get global low_power` output.
func parsePowerSaveFlag(s string) bool {
	return strings.TrimSpace(s) == "1"
}

// parseDumpsysPowerSave looks for a battery-saver-enabled flag set to true.
func parseDumpsysPowerSave(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "mSettingBatterySaverEnabled=") ||
			strings.HasPrefix(l, "mLowPowerModeEnabled=") ||
			strings.HasPrefix(l, "mBatterySaverEnabled=") {
			return strings.HasSuffix(l, "=true")
		}
	}
	return false
}

// runProbe performs a single reachability check (ping or tcp).
func runProbe(p probePolicy) bool {
	if p.Target == "" {
		return false
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	if p.Type == "tcp" {
		conn, err := net.DialTimeout("tcp",
			net.JoinHostPort(p.Target, strconv.Itoa(p.Port)), timeout)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	// ping: -c 1 one packet, -W timeout in seconds (min 1)
	waitSec := (p.TimeoutMS + 999) / 1000
	if waitSec < 1 {
		waitSec = 1
	}
	err := exec.Command("ping", "-c", "1", "-W", strconv.Itoa(waitSec), p.Target).Run()
	return err == nil
}

// ── policy evaluation ───────────────────────────────────────────────────────

// netState is a snapshot of detected network + policy decision (for the UI).
type netState struct {
	Transport   string          `json:"transport"`
	SSID        string          `json:"ssid"`
	PowerSave   bool            `json:"power_save"`
	ProbeOK     bool            `json:"probe_ok"`
	ProbeUp     bool            `json:"probe_up"` // committed hysteresis state
	Terms       map[string]bool `json:"terms"`    // per-term evaluated values
	ShouldRun   bool            `json:"should_run"`
	Enabled     bool            `json:"enabled"`
	Explanation string          `json:"explanation"`
	ExprError   string          `json:"expr_error"`
}

func ssidInWhitelist(ssid string, list []string) bool {
	for _, w := range list {
		if w == ssid {
			return true
		}
	}
	return false
}

// termValues computes the boolean value of each term. A term whose condition is
// disabled evaluates to true (neutral under AND) per the configured semantics.
func termValues(c netPolicyConfig, transport, ssid string, probeUp, powerSave bool) map[string]bool {
	v := map[string]bool{}

	if c.WiFi.Enabled {
		v["wifi"] = transport == "wifi" && ssidInWhitelist(ssid, c.WiFi.Whitelist)
	} else {
		v["wifi"] = true
	}
	if c.Cellular.Enabled {
		v["cellular"] = transport == "cellular"
	} else {
		v["cellular"] = true
	}
	if c.Power.Enabled {
		v["power"] = powerSave
	} else {
		v["power"] = true
	}
	if c.Probe.Enabled {
		v["probe"] = probeUp
	} else {
		v["probe"] = true
	}
	return v
}

// evaluatePolicy parses and evaluates the policy expression against the current
// term values. Returns the decision, a human explanation, and any parse error.
func evaluatePolicy(c netPolicyConfig, terms map[string]bool) (bool, string, error) {
	node, err := parseExpr(c.Expr)
	if err != nil {
		return false, "", err
	}
	result := node.eval(terms)
	parts := make([]string, 0, len(knownTerms))
	for _, t := range knownTerms {
		parts = append(parts, t+"="+strconv.FormatBool(terms[t]))
	}
	expr := strings.TrimSpace(c.Expr)
	if expr == "" {
		expr = "(empty = always run)"
	}
	return result, expr + " → " + strconv.FormatBool(result) +
		"  [" + strings.Join(parts, ", ") + "]", nil
}

// ── monitor ─────────────────────────────────────────────────────────────────

// evalProbeHysteresis runs the probe and updates committed up/down state.
// When commit is false it only reads (does not advance counters) — used by the
// status endpoint so a UI poll can't perturb the monitor's counters.
func evalProbeHysteresis(c netPolicyConfig, commit bool) (raw bool, up bool) {
	if !c.Probe.Enabled {
		return false, true // disabled probe never blocks
	}
	raw = runProbe(c.Probe)
	if !commit {
		return raw, probeState
	}
	if raw {
		probeUpCount++
		probeDownCount = 0
		if probeUpCount >= c.Probe.UpThreshold {
			probeState = true
		}
	} else {
		probeDownCount++
		probeUpCount = 0
		if probeDownCount >= c.Probe.DownThreshold {
			probeState = false
		}
	}
	return raw, probeState
}

// detectNetState builds a full snapshot. commit controls probe hysteresis
// advancement (true only from the monitor loop).
func detectNetState(c netPolicyConfig, commit bool) netState {
	transport := detectTransport()
	ssid := ""
	if transport == "wifi" {
		ssid = detectSSID()
	}
	raw, up := evalProbeHysteresis(c, commit)
	powerSave := false
	if c.Power.Enabled {
		powerSave = detectPowerSave()
	}
	terms := termValues(c, transport, ssid, up, powerSave)
	shouldRun, why, err := evaluatePolicy(c, terms)
	exprErr := ""
	if err != nil {
		exprErr = err.Error()
	}
	return netState{
		Transport: transport, SSID: ssid,
		PowerSave: powerSave,
		ProbeOK:   raw, ProbeUp: up,
		Terms:     terms,
		ShouldRun: shouldRun, Enabled: c.Enabled,
		Explanation: why,
		ExprError:   exprErr,
	}
}

// startNetPolicyMonitor runs the evaluate→reconcile loop until the process exits.
func startNetPolicyMonitor() {
	go func() {
		for {
			c := getNetPolicy()
			interval := time.Duration(c.IntervalSec) * time.Second

			if c.Enabled {
				st := detectNetState(c, true)
				// don't act on a broken expression — leave Syncthing as-is
				if st.ExprError == "" {
					reconcile(st.ShouldRun)
				}
			}

			select {
			case <-time.After(interval):
			case <-npWakeCh: // config changed — re-evaluate now
			}
		}
	}()
}

// reconcile drives Syncthing toward the desired running state.
func reconcile(shouldRun bool) {
	running := isRunning()
	switch {
	case shouldRun && !running:
		_, _ = startSyncthing()
	case !shouldRun && running:
		_ = stopSyncthing()
	}
}

// ── HTTP handlers ─────────────────────────────────────────────────────────

// handleNetPolicyGet returns the config plus a live network snapshot.
func handleNetPolicyGet(c *gin.Context) {
	cfg := getNetPolicy()
	c.JSON(200, gin.H{
		"config": cfg,
		"state":  detectNetState(cfg, false),
	})
}

// handleNetPolicySave validates and persists the config, then re-evaluates.
func handleNetPolicySave(c *gin.Context) {
	var cfg netPolicyConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	if err := saveNetPolicy(cfg); err != nil {
		// invalid expression is a client error; anything else is server-side
		status := 400
		if !strings.Contains(err.Error(), "invalid expression") {
			status = 500
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "network policy saved", "config": getNetPolicy()})
}

// handleNetPolicyScan reports the current transport + SSID for whitelist entry.
func handleNetPolicyScan(c *gin.Context) {
	transport := detectTransport()
	ssid := ""
	if transport == "wifi" {
		ssid = detectSSID()
	}
	c.JSON(200, gin.H{"transport": transport, "ssid": ssid})
}

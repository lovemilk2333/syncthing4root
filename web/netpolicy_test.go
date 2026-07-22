package main

import "testing"

func baseCfg() netPolicyConfig {
	c := defaultNetPolicy()
	return c
}

func TestEvaluatePolicy_NoConditions(t *testing.T) {
	c := baseCfg()
	run, _ := evaluatePolicy(c, "cellular", "", false)
	if !run {
		t.Fatal("no enabled condition should default to run=true")
	}
}

func TestEvaluatePolicy_WifiWhitelist(t *testing.T) {
	c := baseCfg()
	c.WiFi.Enabled = true
	c.WiFi.Whitelist = []string{"Home", "Office"}

	if run, _ := evaluatePolicy(c, "wifi", "Home", false); !run {
		t.Error("whitelisted SSID on wifi should run")
	}
	if run, _ := evaluatePolicy(c, "wifi", "Cafe", false); run {
		t.Error("non-whitelisted SSID should not run")
	}
	if run, _ := evaluatePolicy(c, "cellular", "Home", false); run {
		t.Error("wifi condition must be false when not on wifi")
	}
}

func TestEvaluatePolicy_Cellular(t *testing.T) {
	c := baseCfg()
	c.Cellular.Enabled = true

	if run, _ := evaluatePolicy(c, "cellular", "", false); run {
		t.Error("should stop on cellular")
	}
	if run, _ := evaluatePolicy(c, "wifi", "X", false); !run {
		t.Error("should run when not on cellular")
	}
}

func TestEvaluatePolicy_Probe(t *testing.T) {
	c := baseCfg()
	c.Probe.Enabled = true

	if run, _ := evaluatePolicy(c, "wifi", "X", true); !run {
		t.Error("probe up should run")
	}
	if run, _ := evaluatePolicy(c, "wifi", "X", false); run {
		t.Error("probe down should stop")
	}
}

func TestEvaluatePolicy_CombineAND(t *testing.T) {
	c := baseCfg()
	c.Combine = "AND"
	c.WiFi.Enabled = true
	c.WiFi.Whitelist = []string{"Home"}
	c.Probe.Enabled = true

	// wifi passes, probe fails → AND false
	if run, _ := evaluatePolicy(c, "wifi", "Home", false); run {
		t.Error("AND: one failing condition should stop")
	}
	// both pass → true
	if run, _ := evaluatePolicy(c, "wifi", "Home", true); !run {
		t.Error("AND: all passing should run")
	}
}

func TestEvaluatePolicy_CombineOR(t *testing.T) {
	c := baseCfg()
	c.Combine = "OR"
	c.WiFi.Enabled = true
	c.WiFi.Whitelist = []string{"Home"}
	c.Probe.Enabled = true

	// wifi fails but probe up → OR true
	if run, _ := evaluatePolicy(c, "cellular", "Home", true); !run {
		t.Error("OR: one passing condition should run")
	}
	// both fail → false
	if run, _ := evaluatePolicy(c, "cellular", "Cafe", false); run {
		t.Error("OR: all failing should stop")
	}
}

func TestNormalizeNetPolicy_Clamps(t *testing.T) {
	c := netPolicyConfig{
		Combine:     "weird",
		IntervalSec: 1,
		Probe:       probePolicy{Type: "bogus", Port: 0, TimeoutMS: 10, UpThreshold: 0, DownThreshold: -1},
	}
	n := normalizeNetPolicy(c)
	if n.Combine != "AND" {
		t.Errorf("combine not defaulted: %q", n.Combine)
	}
	if n.IntervalSec != 5 {
		t.Errorf("interval not clamped: %d", n.IntervalSec)
	}
	if n.Probe.Type != "ping" {
		t.Errorf("probe type not defaulted: %q", n.Probe.Type)
	}
	if n.Probe.Port != 22 {
		t.Errorf("port not defaulted: %d", n.Probe.Port)
	}
	if n.Probe.TimeoutMS != 200 {
		t.Errorf("timeout not clamped: %d", n.Probe.TimeoutMS)
	}
	if n.Probe.UpThreshold != 1 || n.Probe.DownThreshold != 1 {
		t.Errorf("thresholds not clamped: %d/%d", n.Probe.UpThreshold, n.Probe.DownThreshold)
	}
	if n.WiFi.Whitelist == nil {
		t.Error("whitelist should be non-nil after normalize")
	}
}

func TestParseSSID(t *testing.T) {
	if got := parseSSIDFromQuoted(`Wifi is connected to "MyNet"`); got != "MyNet" {
		t.Errorf("quoted parse = %q", got)
	}
	if got := parseSSIDFromDumpsys("mWifiInfo SSID: HomeWifi, BSSID: 00:11"); got != "HomeWifi" {
		t.Errorf("dumpsys parse = %q", got)
	}
	if got := parseSSIDFromDumpsys("SSID: <unknown ssid>,"); got != "" {
		t.Errorf("unknown ssid should be empty, got %q", got)
	}
}

func TestIfaceTransport(t *testing.T) {
	cases := map[string]string{
		"wlan0":      "wifi",
		"rmnet_data0": "cellular",
		"ccmni0":     "cellular",
		"eth0":       "other",
		"":           "none",
	}
	for dev, want := range cases {
		if got := ifaceTransport(dev); got != want {
			t.Errorf("ifaceTransport(%q) = %q, want %q", dev, got, want)
		}
	}
}

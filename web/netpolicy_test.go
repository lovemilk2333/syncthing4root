package main

import "testing"

func baseCfg() netPolicyConfig {
	return defaultNetPolicy()
}

// evalWith is a test helper: compute terms then evaluate the expression.
func evalWith(c netPolicyConfig, transport, ssid string, probeUp, powerSave bool) (bool, error) {
	terms := termValues(c, transport, ssid, probeUp, powerSave)
	run, _, err := evaluatePolicy(c, terms)
	return run, err
}

func TestEvaluate_EmptyExpr(t *testing.T) {
	c := baseCfg() // expr == "" → always run
	run, err := evalWith(c, "cellular", "", false, false)
	if err != nil || !run {
		t.Fatalf("empty expr should always run (run=%v err=%v)", run, err)
	}
}

func TestTermValues_DisabledIsTrue(t *testing.T) {
	c := baseCfg() // all conditions disabled
	terms := termValues(c, "cellular", "Cafe", false, true)
	for _, k := range knownTerms {
		if !terms[k] {
			t.Errorf("disabled term %q should be true, got false", k)
		}
	}
}

func TestTermValues_Wifi(t *testing.T) {
	c := baseCfg()
	c.WiFi.Enabled = true
	c.WiFi.Whitelist = []string{"Home"}

	if v := termValues(c, "wifi", "Home", false, false)["wifi"]; !v {
		t.Error("whitelisted SSID on wifi → wifi term true")
	}
	if v := termValues(c, "wifi", "Cafe", false, false)["wifi"]; v {
		t.Error("non-whitelisted SSID → wifi term false")
	}
	if v := termValues(c, "cellular", "Home", false, false)["wifi"]; v {
		t.Error("not on wifi → wifi term false")
	}
}

func TestTermValues_Cellular(t *testing.T) {
	c := baseCfg()
	c.Cellular.Enabled = true
	if v := termValues(c, "cellular", "", false, false)["cellular"]; !v {
		t.Error("on cellular → cellular term true")
	}
	if v := termValues(c, "wifi", "X", false, false)["cellular"]; v {
		t.Error("not on cellular → cellular term false")
	}
}

func TestTermValues_Power(t *testing.T) {
	c := baseCfg()
	c.Power.Enabled = true
	if v := termValues(c, "wifi", "X", false, true)["power"]; !v {
		t.Error("battery saver on → power term true")
	}
	if v := termValues(c, "wifi", "X", false, false)["power"]; v {
		t.Error("battery saver off → power term false")
	}
}

func TestEvaluate_ExpressionCombos(t *testing.T) {
	c := baseCfg()
	c.WiFi.Enabled = true
	c.WiFi.Whitelist = []string{"Home"}
	c.Cellular.Enabled = true
	c.Probe.Enabled = true

	// "run on whitelisted wifi, not on cellular, and probe up"
	c.Expr = "wifi AND NOT cellular AND probe"
	if run, _ := evalWith(c, "wifi", "Home", true, false); !run {
		t.Error("all satisfied → run")
	}
	if run, _ := evalWith(c, "wifi", "Home", false, false); run {
		t.Error("probe down → stop")
	}
	if run, _ := evalWith(c, "cellular", "Home", true, false); run {
		t.Error("on cellular → stop")
	}

	// nested + OR
	c.Expr = "wifi AND (probe OR NOT cellular)"
	if run, _ := evalWith(c, "wifi", "Home", false, false); !run {
		t.Error("wifi && (false || not-cellular=true) → run")
	}
}

func TestEvaluate_BrokenExprReturnsError(t *testing.T) {
	c := baseCfg()
	c.Expr = "wifi AND"
	if _, err := evalWith(c, "wifi", "Home", true, false); err == nil {
		t.Error("dangling AND should parse-error")
	}
}

func TestMigrateLegacyExpr(t *testing.T) {
	raw := []byte(`{"combine":"OR"}`)
	c := baseCfg()
	c.WiFi.Enabled = true
	c.Cellular.Enabled = true
	c.Power.Enabled = true
	c.Probe.Enabled = true
	got := migrateLegacyExpr(raw, c)
	want := "wifi OR NOT cellular OR NOT power OR probe"
	if got != want {
		t.Errorf("migrate = %q, want %q", got, want)
	}

	// AND default, only wifi+probe
	raw2 := []byte(`{"combine":"AND"}`)
	c2 := baseCfg()
	c2.WiFi.Enabled = true
	c2.Probe.Enabled = true
	if got := migrateLegacyExpr(raw2, c2); got != "wifi AND probe" {
		t.Errorf("migrate2 = %q", got)
	}

	// nothing enabled → empty
	if got := migrateLegacyExpr([]byte(`{}`), baseCfg()); got != "" {
		t.Errorf("migrate empty = %q, want empty", got)
	}
}

func TestNormalizeNetPolicy_Clamps(t *testing.T) {
	c := netPolicyConfig{
		IntervalSec: 1,
		Expr:        "  wifi  ",
		Probe:       probePolicy{Type: "bogus", Port: 0, TimeoutMS: 10, UpThreshold: 0, DownThreshold: -1},
	}
	n := normalizeNetPolicy(c)
	if n.Expr != "wifi" {
		t.Errorf("expr not trimmed: %q", n.Expr)
	}
	if n.IntervalSec != 5 {
		t.Errorf("interval not clamped: %d", n.IntervalSec)
	}
	if n.Probe.Type != "ping" || n.Probe.Port != 22 || n.Probe.TimeoutMS != 200 {
		t.Errorf("probe not normalized: %+v", n.Probe)
	}
	if n.Probe.UpThreshold != 1 || n.Probe.DownThreshold != 1 {
		t.Errorf("thresholds not clamped: %d/%d", n.Probe.UpThreshold, n.Probe.DownThreshold)
	}
	if n.WiFi.Whitelist == nil {
		t.Error("whitelist should be non-nil")
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
		"wlan0":       "wifi",
		"rmnet_data0": "cellular",
		"ccmni0":      "cellular",
		"eth0":        "other",
		"":            "none",
	}
	for dev, want := range cases {
		if got := ifaceTransport(dev); got != want {
			t.Errorf("ifaceTransport(%q) = %q, want %q", dev, got, want)
		}
	}
}

func TestParsePowerSave(t *testing.T) {
	if !parsePowerSaveFlag("1\n") {
		t.Error("low_power=1 should be on")
	}
	if parsePowerSaveFlag("0\n") {
		t.Error("low_power=0 should be off")
	}
	if !parseDumpsysPowerSave("  mSettingBatterySaverEnabled=true") {
		t.Error("dumpsys battery-saver true not detected")
	}
	if parseDumpsysPowerSave("  mLowPowerModeEnabled=false") {
		t.Error("dumpsys battery-saver false misread as on")
	}
}

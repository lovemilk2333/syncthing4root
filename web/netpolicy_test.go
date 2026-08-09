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

func TestTermValues_Disabled(t *testing.T) {
	c := baseCfg() // all conditions disabled
	terms := termValues(c, "cellular", "Cafe", false, true)
	// wifi/cellular/probe default true; power defaults false
	for _, k := range []string{"wifi", "cellular", "probe"} {
		if !terms[k] {
			t.Errorf("disabled term %q should be true, got false", k)
		}
	}
	if terms["power"] {
		t.Error("disabled power term should be false")
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

func TestParseDevField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"8.8.8.8 via 192.168.1.1 dev wlan0 src 192.168.1.100 uid 0", "wlan0"},
		{"8.8.8.8 via 198.18.0.1 dev meta src 10.0.0.151", "meta"},
		{"", ""},
		{"garbage output without dev", ""},
	}
	for _, c := range cases {
		if got := parseDevField(c.in); got != c.want {
			t.Errorf("parseDevField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClassifyUpInterfaces(t *testing.T) {
	// fixture mirrors the user's device: wlan0 + ccmni0 + meta all UP
	fixture := `
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    inet 127.0.0.1/8 scope host lo
40: wlan0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc htb state UP group default qlen 3000
    inet 10.0.0.151/16 brd 10.0.255.255 scope global wlan0
14: ccmni0: <NOARP,UP,LOWER_UP> mtu 1460 qdisc mq state UNKNOWN group default qlen 1000
    inet 10.30.0.5/30 scope global ccmni0
50: meta: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 9000 qdisc pfifo_fast state UNKNOWN group default qlen 500
    inet 198.18.0.1/30 brd 198.18.0.3 scope global meta
47: p2p0: <BROADCAST,MULTICAST> mtu 1500 qdisc mq state DOWN group default qlen 1000
    inet 192.168.49.1/24 brd 192.168.49.255 scope global p2p0
`
	cases := []struct {
		name, in, wantT, wantDev string
	}{
		{"wifi preferred", fixture, "wifi", "wlan0"},
		{"only cellular", "14: ccmni0: <NOARP,UP,LOWER_UP> mtu 1460\n    inet 10.30.0.5/30 scope global ccmni0\n", "cellular", "ccmni0"},
		{"only proxy iface", "50: meta: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 9000\n    inet 198.18.0.1/30 scope global meta\n", "other", "meta"},
		{"only loopback", "1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536\n    inet 127.0.0.1/8 scope host lo\n", "none", ""},
		{"down iface with inet is ignored", "47: p2p0: <BROADCAST,MULTICAST> mtu 1500 state DOWN\n    inet 192.168.49.1/24 scope global p2p0\n", "none", ""},
		{"empty", "", "none", ""},
	}
	for _, c := range cases {
		gotT, gotDev := classifyUpInterfaces(c.in)
		if gotT != c.wantT || gotDev != c.wantDev {
			t.Errorf("%s: classifyUpInterfaces = (%q, %q), want (%q, %q)",
				c.name, gotT, gotDev, c.wantT, c.wantDev)
		}
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
		"swlan0":      "wifi",
		"mlan0":       "wifi",
		"wifi0":       "wifi",
		"wl0":         "wifi",
		"ap0":         "wifi",
		"rmnet_data0": "cellular",
		"ccmni0":      "cellular",
		"clat4":       "cellular",
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

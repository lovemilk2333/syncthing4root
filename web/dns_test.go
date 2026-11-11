package main

import (
	"net"
	"net/netip"
	"testing"
)

func TestValidateDNSServer(t *testing.T) {
	valid := []string{
		"", // auto
		"192.168.1.1",
		"192.168.1.1:5353",
		"223.5.5.5",
		"https://doh.pub/dns-query",
		"https://1.1.1.1/dns-query",
		"tls://1.1.1.1",
		"tls://1.1.1.1:853",
		"tls://dns.google",
		"tls://dns.google:8853",
		"tls://[2606:4700:4700::1111]:853", // bracketed IPv6 needs a port
		"2606:4700:4700::1111",             // bare IPv6
	}
	for _, s := range valid {
		if err := validateDNSServer(s); err != nil {
			t.Errorf("validateDNSServer(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{
		"doh.pub", // hostname without https:// is not a server address
		"192.168.1.999",
		"192.168.1.1:99999",
		"http://doh.pub/dns-query", // DoH must be https
		"tls://",                   // empty host
		"tls://foo bar",            // no spaces in hostnames
		"tls://1.1.1.1:99999",
		"tls://[2606::1]",   // bracketed IPv6 without a port is malformed
		"tls://-dns.google", // invalid hostname
	}
	for _, s := range invalid {
		if err := validateDNSServer(s); err == nil {
			t.Errorf("validateDNSServer(%q) should fail", s)
		}
	}
}

func TestParseDNSList(t *testing.T) {
	in := `Active default network: 100
    DnsAddresses: [192.168.1.1, 8.8.8.8]
    LinkAddresses: [10.0.0.151/16]
    Clatd: 198.18.0.1`
	got := parseDNSList(in)
	want := []string{"192.168.1.1", "8.8.8.8", "10.0.0.151", "198.18.0.1"}
	if len(got) != len(want) {
		t.Fatalf("parseDNSList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseDNSList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestViaIPv4s(t *testing.T) {
	in := "8.8.8.8 via 192.168.1.1 dev wlan0 src 10.0.0.151 uid 0"
	got := viaIPv4s(in)
	if len(got) != 1 || got[0] != "192.168.1.1" {
		t.Errorf("viaIPv4s = %v, want [192.168.1.1]", got)
	}
}

func TestDedupeDNS(t *testing.T) {
	in := []string{"192.168.1.1", "8.8.8.8", "192.168.1.1", "garbage", "127.0.0.1", "198.18.0.1"}
	got := dedupeDNS(in)
	want := []string{"192.168.1.1", "8.8.8.8", "127.0.0.1", "198.18.0.1"}
	if len(got) != len(want) {
		t.Fatalf("dedupeDNS = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dedupeDNS[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsLAN(t *testing.T) {
	lan := []string{"192.168.1.1", "10.0.0.2", "172.16.5.5"}
	for _, s := range lan {
		if !isLAN(net.ParseIP(s)) {
			t.Errorf("isLAN(%s) = false, want true", s)
		}
	}
	notLan := []string{
		"8.8.8.8",     // public
		"223.5.5.5",   // public
		"100.64.0.1",  // CGNAT — carrier space, not LAN
		"198.18.0.1",  // Clash fake-IP
		"127.0.0.53",  // loopback
		"169.254.0.1", // link-local
	}
	for _, s := range notLan {
		if isLAN(net.ParseIP(s)) {
			t.Errorf("isLAN(%s) = true, want false", s)
		}
	}
}

func TestSysAddrStrings(t *testing.T) {
	addrs := []netip.AddrPort{
		netip.MustParseAddrPort("192.168.1.1:53"),
		netip.MustParseAddrPort("8.8.8.8:53"),
		netip.MustParseAddrPort("1.1.1.1:5353"), // non-standard port — dropped
		netip.MustParseAddrPort("[2606:4700:4700::1111]:53"),
	}
	got := sysAddrStrings(addrs)
	want := []string{"192.168.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	if len(got) != len(want) {
		t.Fatalf("sysAddrStrings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sysAddrStrings[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildDNSQuery(t *testing.T) {
	q := buildDNSQuery(0x1234, "api.github.com")
	if len(q) != 32 {
		t.Fatalf("query length = %d, want 32", len(q))
	}
	if q[0] != 0x12 || q[1] != 0x34 || q[2] != 0x01 || q[3] != 0x00 || q[5] != 0x01 {
		t.Errorf("bad header: %x", q[:12])
	}
	if string(q[13:16]) != "api" || string(q[17:23]) != "github" || string(q[24:27]) != "com" {
		t.Errorf("bad qname: %q", q[12:28])
	}
	if q[28] != 0 || q[29] != 1 || q[30] != 0 || q[31] != 1 {
		t.Errorf("bad QTYPE/QCLASS: %x", q[28:32])
	}
}

// response with a compressed CNAME answer before the A record must still
// yield the A record; NXDOMAIN and empty answers must fail
func TestParseAAnswer(t *testing.T) {
	q := buildDNSQuery(0x1234, "api.github.com")
	resp := append([]byte{}, q...)
	resp[2], resp[3] = 0x81, 0x80 // QR=1, RD=1, RA=1, rcode=0
	resp[6], resp[7] = 0x00, 0x02 // ancount=2

	// answer 1: CNAME github.com — owner compressed to qname at offset 12,
	// rdlen 12 ("github.com" = 1+6+1+3+1)
	resp = append(resp,
		0xC0, 0x0C, 0x00, 0x05, 0x00, 0x01, 0, 0, 0, 60, 0x00, 0x0C,
		0x06, 'g', 'i', 't', 'h', 'u', 'b', 0x03, 'c', 'o', 'm', 0x00,
	)
	// answer 2: A 20.205.243.168
	resp = append(resp,
		0xC0, 0x0C, 0x00, 0x01, 0x00, 0x01, 0, 0, 0, 60, 0x00, 0x04,
		20, 205, 243, 168,
	)
	ip, err := parseAAnswer(resp)
	if err != nil {
		t.Fatal(err)
	}
	if ip != "20.205.243.168" {
		t.Errorf("parseAAnswer = %s, want 20.205.243.168", ip)
	}

	bad := append([]byte{}, resp...)
	bad[3] = 0x83 // NXDOMAIN
	if _, err := parseAAnswer(bad); err == nil {
		t.Error("NXDOMAIN should fail")
	}
	none := append([]byte{}, resp...)
	none[6], none[7] = 0, 0 // no answers
	if _, err := parseAAnswer(none); err == nil {
		t.Error("empty answer section should fail")
	}
}

// only LAN (private) servers survive; public, fake-IP, loopback, link-local
// and duplicate addresses are all dropped
func TestFilterDNSList(t *testing.T) {
	in := []string{
		"8.8.8.8",     // public — dropped
		"223.5.5.5",   // public — dropped
		"192.168.1.1", // LAN — kept
		"198.18.0.1",  // Clash fake-IP — dropped (not private)
		"127.0.0.53",  // loopback — dropped
		"169.254.0.1", // link-local — dropped
		"192.168.1.1", // duplicate — dropped
		"10.0.0.2",    // LAN — kept
	}
	got := filterDNSList(in)
	want := []string{"192.168.1.1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("filterDNSList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("filterDNSList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

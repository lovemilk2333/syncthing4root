package main

// Outbound DNS handling for the update path (version check + download).
//
// On Android the pure-Go resolver reads /system/etc/resolv.conf, which
// netd-driven builds keep empty — a root background daemon (this web server)
// then fails every hostname lookup. Rather than depending on that, resolution
// goes through a configurable resolver:
//
//   - empty            → system resolver, then auto-detected LAN DNS servers
//                        (dumpsys connectivity / getprop / default-route
//                        gateway / resolv.conf), then public DoH
//                        (doh.pub, dns.alidns.com, dns.cloudflare.com) and
//                        DoT (tls://1.1.1.1) — DoH/DoT survive Clash TUN,
//                        which hijacks every UDP:53 packet
//   - "ip" / "ip:port" → query that DNS server directly (UDP/TCP 53)
//   - "https://…"      → DoH (DNS-over-HTTPS JSON API)
//   - "tls://…"        → DoT (DNS-over-TLS, RFC 7858, default port 853)
//
// The setting lives in .dns_config next to the module data and applies to
// every request made by the update code.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var dnsCfg atomic.Value // holds dnsConfig

func init() {
	dnsCfg.Store(dnsConfig{}) // sane default; loadDNSConfig overrides at startup
}

type dnsConfig struct {
	Server string // "" = auto; "ip[:port]"; "https://…" DoH URL
}

func dnsConfigPath() string {
	return filepath.Join(syncthingDir, ".dns_config")
}

func loadDNSConfig() {
	raw, _ := os.ReadFile(dnsConfigPath())
	dnsCfg.Store(dnsConfig{Server: strings.TrimSpace(string(raw))})
}

// hostnameRe matches a plain DNS hostname (no scheme, no port, no spaces).
var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)

// validateDNSServer checks a user-supplied value before saving.
func validateDNSServer(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if rest, ok := strings.CutPrefix(s, "https://"); ok {
		// parse with the scheme restored — url.Parse on the bare rest would
		// treat "doh.pub/dns-query" as a path and report no host
		u, err := url.Parse("https://" + rest)
		if err != nil || u.Host == "" {
			return fmt.Errorf("invalid DoH URL: %q", s)
		}
		return nil
	}
	if rest, ok := strings.CutPrefix(s, "tls://"); ok {
		return validateDoTServer(rest)
	}
	if net.ParseIP(s) != nil {
		return nil // bare IPv4/IPv6
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return fmt.Errorf("DNS server must be an IP, ip:port, https:// DoH URL, or tls:// DoT server")
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("DNS server must be an IP address: %q", host)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid port: %q", port)
	}
	return nil
}

// validateDoTServer checks the part after tls:// — a hostname or IP,
// optionally with a port (default 853). DoT allows hostnames (resolved via
// bootstrapIP), unlike plain UDP/TCP DNS servers which need a dialable IP.
func validateDoTServer(s string) error {
	if net.ParseIP(s) != nil {
		return nil // bare IPv4/IPv6
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		if hostnameRe.MatchString(s) {
			return nil // bare hostname
		}
		return fmt.Errorf("invalid DoT server: %q", s)
	}
	if host == "" || (net.ParseIP(host) == nil && !hostnameRe.MatchString(host)) {
		return fmt.Errorf("invalid DoT server: %q", s)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid port: %q", port)
	}
	return nil
}

func saveDNSConfig(server string) error {
	server = strings.TrimSpace(server)
	if err := validateDNSServer(server); err != nil {
		return err
	}
	if err := os.WriteFile(dnsConfigPath(), []byte(server+"\n"), 0644); err != nil {
		return err
	}
	dnsCfg.Store(dnsConfig{Server: server})
	return nil
}

// ── resolution ─────────────────────────────────────────────────────────────

// lookupIP resolves host (a domain name) to an IPv4 address. Plain IPs pass
// through. The strategy follows dnsCfg (see file header).
func lookupIP(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	cfg := dnsCfg.Load().(dnsConfig)
	switch {
	case strings.HasPrefix(cfg.Server, "https://"):
		return dohLookup(ctx, cfg.Server, host)
	case strings.HasPrefix(cfg.Server, "tls://"):
		return dotLookup(ctx, cfg.Server, host)
	case cfg.Server != "":
		return serverLookup(ctx, cfg.Server, host)
	}
	// no explicit config: system resolver → detected LAN DNS → public DoH →
	// DoT. The DoH/DoT steps matter: Clash TUN hijacks every UDP:53 packet
	// (returning fake IPs or nothing at all), while DoH rides HTTPS on :443
	// and DoT rides TLS on :853 — both untouched by the hijack.
	ip, err := systemLookup(ctx, host)
	if err != nil {
		for _, srv := range detectLocalDNSServers(ctx) {
			if ip, err = serverLookup(ctx, srv, host); err == nil {
				break
			}
		}
	}
	if err != nil {
		// domestic DoH endpoints first — reachable without a proxy
		for _, doh := range []string{
			"https://doh.pub/dns-query",
			"https://dns.alidns.com/dns-query",
			"https://dns.cloudflare.com/dns-query",
		} {
			if ip, err = dohLookup(ctx, doh, host); err == nil {
				break
			}
		}
	}
	if err != nil {
		if ip, err = dotLookup(ctx, "tls://1.1.1.1", host); err == nil {
			return ip, nil
		}
	}
	return ip, err
}

func systemLookup(ctx context.Context, host string) (string, error) {
	return firstIPv4(ctx, net.DefaultResolver.LookupIPAddr, host)
}

// serverLookup queries a specific DNS server (UDP, falling back to TCP inside
// net.Resolver) instead of the system resolver.
func serverLookup(ctx context.Context, server, host string) (string, error) {
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
	return firstIPv4(ctx, r.LookupIPAddr, host)
}

type ipAddrLookup func(ctx context.Context, host string) ([]net.IPAddr, error)

func firstIPv4(ctx context.Context, lookup ipAddrLookup, host string) (string, error) {
	ips, err := lookup(ctx, host)
	if err != nil {
		return "", err
	}
	for _, ia := range ips {
		if v4 := ia.IP.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address for %s", host)
}

// dohLookup resolves host via a DNS-over-HTTPS JSON API (RFC 8484-adjacent:
// dns.google, cloudflare-dns.com, doh.pub, dns.alidns.com all speak it).
func dohLookup(ctx context.Context, dohURL, host string) (string, error) {
	u, err := url.Parse(dohURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("name", host)
	q.Set("type", "A")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := (&http.Client{Timeout: 10 * time.Second, Transport: dohTransport()}).Do(req)
	if err != nil {
		return "", fmt.Errorf("DoH %s: %w", dohURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DoH %s: HTTP %d", dohURL, resp.StatusCode)
	}
	var d struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"` // 1 = A
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return "", err
	}
	if d.Status != 0 { // 0 = NOERROR
		return "", fmt.Errorf("DoH %s: status %d for %s", dohURL, d.Status, host)
	}
	for _, a := range d.Answer {
		if a.Type == 1 && net.ParseIP(a.Data) != nil {
			return a.Data, nil
		}
	}
	return "", fmt.Errorf("DoH %s: no A record for %s", dohURL, host)
}

// bootstrapIP resolves the hostname of a DoH/DoT server itself, using the
// system resolver first and falling back to detected LAN DNS — never
// recursing into DoH/DoT, which would need DNS to work to be a DNS fallback.
func bootstrapIP(ctx context.Context, host string) (string, error) {
	ip, err := systemLookup(ctx, host)
	if err != nil {
		for _, srv := range detectLocalDNSServers(ctx) {
			if ip, err = serverLookup(ctx, srv, host); err == nil {
				break
			}
		}
	}
	return ip, err
}

// dotLookup resolves host via a DNS-over-TLS server (RFC 7858), e.g.
// tls://1.1.1.1 (port 853). DoT is just DNS over TCP with TLS, so the whole
// exchange fits in the stdlib: build a query, TLS-dial (Android CA pool,
// same trust problem as every other TLS here), send, parse the first A.
func dotLookup(ctx context.Context, dotServer, host string) (string, error) {
	srv := strings.TrimPrefix(dotServer, "tls://")
	hostPart, port, err := net.SplitHostPort(srv)
	if err != nil {
		hostPart, port = srv, "853"
	}
	addr := net.JoinHostPort(hostPart, port)
	if net.ParseIP(hostPart) == nil {
		ip, err := bootstrapIP(ctx, hostPart)
		if err != nil {
			return "", fmt.Errorf("DoT %s: %w", dotServer, err)
		}
		addr = net.JoinHostPort(ip, port)
	}

	id := uint16(rand.Uint32())
	query := buildDNSQuery(id, host)
	d := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config: &tls.Config{
			ServerName: hostPart,
			RootCAs:    systemCertPool(),
		},
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("DoT %s: %w", dotServer, err)
	}
	defer conn.Close()
	deadline, has := ctx.Deadline()
	if !has {
		deadline = time.Now().Add(10 * time.Second)
	}
	conn.SetDeadline(deadline)

	buf := make([]byte, 2+len(query))
	buf[0], buf[1] = byte(len(query)>>8), byte(len(query))
	copy(buf[2:], query)
	if _, err := conn.Write(buf); err != nil {
		return "", fmt.Errorf("DoT %s: %w", dotServer, err)
	}
	var lenBuf [2]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return "", fmt.Errorf("DoT %s: %w", dotServer, err)
	}
	resp := make([]byte, int(lenBuf[0])<<8|int(lenBuf[1]))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return "", fmt.Errorf("DoT %s: %w", dotServer, err)
	}
	if len(resp) < 2 || uint16(resp[0])<<8|uint16(resp[1]) != id {
		return "", fmt.Errorf("DoT %s: mismatched transaction ID", dotServer)
	}
	ip, err := parseAAnswer(resp)
	if err != nil {
		return "", fmt.Errorf("DoT %s: %w", dotServer, err)
	}
	return ip, nil
}

// buildDNSQuery returns an A query (RD set) for host with transaction id.
func buildDNSQuery(id uint16, host string) []byte {
	buf := []byte{byte(id >> 8), byte(id), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for label := range strings.SplitSeq(host, ".") {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	return append(buf, 0, 0, 1, 0, 1) // root, QTYPE=A, QCLASS=IN
}

// parseAAnswer extracts the first A record from a DNS response (header +
// question + answers). Answer names may use compression pointers, which are
// skipped; anything that is not an A record is skipped too.
func parseAAnswer(resp []byte) (string, error) {
	if len(resp) < 12 {
		return "", fmt.Errorf("truncated DNS response")
	}
	if rcode := resp[3] & 0x0F; rcode != 0 {
		return "", fmt.Errorf("DNS rcode %d", rcode)
	}
	ancount := int(resp[6])<<8 | int(resp[7])

	off := 12
	if _, next, err := skipDNSName(resp, off); err != nil {
		return "", err
	} else {
		off = next
	}
	off += 4 // QTYPE + QCLASS

	for range ancount {
		if _, next, err := skipDNSName(resp, off); err != nil {
			return "", err
		} else {
			off = next
		}
		if off+10 > len(resp) {
			return "", fmt.Errorf("truncated DNS answer")
		}
		atype := int(resp[off])<<8 | int(resp[off+1])
		rdlen := int(resp[off+8])<<8 | int(resp[off+9])
		off += 10
		if atype == 1 && rdlen == 4 && off+4 <= len(resp) {
			return net.IPv4(resp[off], resp[off+1], resp[off+2], resp[off+3]).String(), nil
		}
		off += rdlen
	}
	return "", fmt.Errorf("no A record in DNS response")
}

// skipDNSName skips a (possibly compressed) DNS name at off and returns the
// byte offset after it; pointers are followed with a jump cap so a malformed
// response cannot loop forever.
func skipDNSName(b []byte, off int) (string, int, error) {
	name := ""
	next := off
	jumped := false
	for range 64 {
		if off >= len(b) {
			return "", 0, fmt.Errorf("DNS name out of range")
		}
		switch n := int(b[off]); {
		case n == 0:
			if !jumped {
				next = off + 1
			}
			return name, next, nil
		case n&0xC0 == 0xC0:
			if off+1 >= len(b) {
				return "", 0, fmt.Errorf("bad DNS pointer")
			}
			if !jumped {
				next = off + 2
			}
			off = n&0x3F<<8 | int(b[off+1])
			jumped = true
		default:
			if off+1+n > len(b) {
				return "", 0, fmt.Errorf("DNS label out of range")
			}
			if name != "" {
				name += "."
			}
			name += string(b[off+1 : off+1+n])
			off += 1 + n
		}
	}
	return "", 0, fmt.Errorf("too many DNS compression jumps")
}

// dohTransport dials the DoH server itself via bootstrapIP — otherwise DoH
// would need DNS to work to be a DNS fallback.
func dohTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: systemCertPool()}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ip, err := bootstrapIP(ctx, host)
		if err != nil {
			return nil, err
		}
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ip, port))
	}
	return t
}

// ── LAN DNS detection ──────────────────────────────────────────────────────

var ipv4Re = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// detectDNSRaw gathers DNS server candidates of the active network in
// discovery order: dumpsys connectivity, getprop net.dns*, default-route
// gateway, resolv.conf. ctx bounds the exec calls so a wedged dumpsys can
// never hang a dial.
func detectDNSRaw(ctx context.Context) []string {
	var raw []string

	// 1. dumpsys connectivity (root daemon) — DnsAddresses/DNSServers lists
	if b, err := exec.CommandContext(ctx, "dumpsys", "connectivity").Output(); err == nil {
		raw = append(raw, parseDNSList(string(b))...)
	}
	// 2. getprop net.dns* (legacy but still populated on many ROMs)
	for _, p := range []string{"net.dns1", "net.dns2", "net.eth0.dns1"} {
		if v := getprop(ctx, p); v != "" {
			raw = append(raw, v)
		}
	}
	// 3. default-route gateway — usually the router, which also serves DNS
	if b, err := exec.CommandContext(ctx, "ip", "route", "get", "8.8.8.8").Output(); err == nil {
		raw = append(raw, viaIPv4s(string(b))...)
	}
	// 4. classic resolv.conf
	if b, err := os.ReadFile("/system/etc/resolv.conf"); err == nil {
		raw = append(raw, parseDNSList(string(b))...)
	}

	return raw
}

// detectLocalDNSServers returns the DNS servers auto mode actually queries.
func detectLocalDNSServers(ctx context.Context) []string {
	return filterDNSList(detectDNSRaw(ctx))
}

// dnsServerInfo describes one detected candidate for the UI: LAN marks the
// ones auto mode uses; the rest are ignored and shown with a strikethrough.
type dnsServerInfo struct {
	IP  string `json:"ip"`
	LAN bool   `json:"lan"`
}

// detectDNSServerInfo returns every detected candidate, deduplicated, in
// discovery order, marked with whether auto mode would use it.
func detectDNSServerInfo(ctx context.Context) []dnsServerInfo {
	out := []dnsServerInfo{}
	for _, s := range dedupeDNS(detectDNSRaw(ctx)) {
		out = append(out, dnsServerInfo{IP: s, LAN: isLAN(net.ParseIP(s))})
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// dedupeDNS removes duplicates (and non-IP junk) while keeping discovery order.
func dedupeDNS(raw []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range raw {
		ip := net.ParseIP(s)
		if ip == nil || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// isLAN reports whether a DNS server survives the auto-mode filter. Go's
// IsPrivate is RFC 1918 (10/8, 172.16/12, 192.168/16) only — loopback
// (127/8), link-local (169.254/16), CGNAT (100.64/10) and the Clash fake-IP
// range (198.18/15) are all excluded. And since the auto chain tries each
// server in order and fails over, a borderline address is harmless either
// way — no extra exclusions needed.
func isLAN(ip net.IP) bool {
	return ip.IsPrivate()
}

// filterDNSList keeps only LAN (private) DNS servers, deduplicated, in
// discovery order, capped at 4. Public servers are dropped on purpose: on the
// affected devices every UDP:53 packet is hijacked (Clash TUN fake-IP), so a
// public DNS reported by dumpsys would just answer with fake IPs or nothing
// at all. Networks without a LAN DNS fall through to the public DoH fallback
// in lookupIP, which is immune to the hijack.
func filterDNSList(raw []string) []string {
	var out []string
	for _, s := range dedupeDNS(raw) {
		if isLAN(net.ParseIP(s)) {
			out = append(out, s)
		}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

var viaIPv4Re = regexp.MustCompile(`via (\d{1,3}(?:\.\d{1,3}){3})`)

func viaIPv4s(s string) []string {
	var out []string
	for _, m := range viaIPv4Re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// parseDNSList extracts every IPv4 address from a text blob (dumpsys
// connectivity, resolv.conf). No semantic filtering here — callers order and
// filter the result.
func parseDNSList(s string) []string {
	var out []string
	for _, m := range ipv4Re.FindAllString(s, -1) {
		if net.ParseIP(m) != nil {
			out = append(out, m)
		}
	}
	return out
}

func getprop(ctx context.Context, key string) string {
	b, err := exec.CommandContext(ctx, "getprop", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

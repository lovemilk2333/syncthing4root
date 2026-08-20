package main

// Pure-Go update: fetch the latest syncthing version and replace the binary.
// The old shell-based update (update.sh) silently failed in the web server's
// runtime environment, which frequently has no curl or wget on PATH (the
// module's service.d process only sees the boot PATH, while install-time
// customize.sh runs under the manager with a full busybox environment).
// Everything here runs on the Go stdlib, with per-source diagnostics surfaced
// in the API error so failures are explainable.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"crypto/tls"
	"crypto/x509"
)

// githubRelease covers both the GitHub API /releases/latest and the
// upgrades.syncthing.net mirror metadata.
type githubRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
}

type versionSource struct {
	label string
	url   string
	parse func(body []byte) string
}

func parseAPITag(body []byte) string {
	var rel githubRelease
	if json.Unmarshal(body, &rel) != nil || rel.TagName == "" {
		return ""
	}
	return strings.TrimPrefix(rel.TagName, "v")
}

func parseMirrorTag(body []byte) string {
	var rels []githubRelease
	if json.Unmarshal(body, &rels) != nil {
		return ""
	}
	for _, r := range rels {
		if !r.Prerelease && r.TagName != "" {
			return strings.TrimPrefix(r.TagName, "v")
		}
	}
	return ""
}

// pageTagRe extracts the version from a GitHub releases page (used through the
// ghfast.top proxy, which serves the page but not the API).
var pageTagRe = regexp.MustCompile(`releases/tag/v([0-9][0-9.]*)`)

func parsePageTag(body []byte) string {
	if m := pageTagRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

// systemCertPool returns the platform CA store. Go's SystemCertPool only
// knows standard Linux paths (/etc/ssl/certs/...), which Android doesn't
// have — a root Go daemon on Android otherwise ends up with ZERO trust
// anchors and fails every TLS handshake with "certificate signed by unknown
// authority". Android keeps its CAs as PEM files in cacerts directories.
func systemCertPool() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	loadAndroidCertDirs(pool,
		"/system/etc/security/cacerts",        // Android ≤13
		"/apex/com.android.conscrypt/cacerts", // Android 14+
	)
	return pool
}

func loadAndroidCertDirs(pool *x509.CertPool, dirs ...string) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // not present on this platform
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			pool.AppendCertsFromPEM(b)
		}
	}
}

// updateTransport dials via the configured DNS resolver (see dns.go) instead
// of the system resolver, which is frequently broken for root daemons on
// Android (empty resolv.conf under netd), and verifies TLS against the
// platform CA store (Android's cacerts — Go's system pool misses it).
var updateTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: systemCertPool()}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ip, err := lookupIP(ctx, host)
		if err != nil {
			return nil, err
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ip, port))
	}
	return t
}()

// httpGet fetches a small response (API metadata / HTML page) with a short
// timeout. Go transparently decompresses gzip (GitHub API sends it).
func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub's API rejects requests without a User-Agent.
	req.Header.Set("User-Agent", "syncthing4root")
	client := &http.Client{Timeout: 20 * time.Second, Transport: updateTransport}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %.100s", resp.StatusCode, body)
	}
	return body, nil
}

// fetchLatestVersion tries each source in order until one yields a version.
// Every failure is recorded with its reason (DNS error, connection refused,
// HTTP status, empty body...) so the final error shows exactly what happened.
func fetchLatestVersion(ctx context.Context) (string, error) {
	sources := []versionSource{
		{"github api", "https://api.github.com/repos/syncthing/syncthing/releases/latest", parseAPITag},
		{"upgrades mirror", "https://upgrades.syncthing.net/meta.json", parseMirrorTag},
		{"ghfast.top", "https://ghfast.top/https://github.com/syncthing/syncthing/releases/latest", parsePageTag},
	}
	var diags []string
	for _, s := range sources {
		body, err := httpGet(ctx, s.url)
		if err != nil {
			diags = append(diags, fmt.Sprintf("[%s] %v", s.label, err))
			continue
		}
		if v := s.parse(body); v != "" {
			return v, nil
		}
		snippet := strings.ReplaceAll(strings.TrimSpace(string(body)), "\n", " ")
		diags = append(diags, fmt.Sprintf("[%s] no version in %d bytes: %.100s", s.label, len(body), snippet))
	}
	return "", fmt.Errorf("failed to get latest version: %s", strings.Join(diags, "; "))
}

// installedVersion runs `syncthing --version` and returns the version without
// the leading "v", or "" when the binary is missing or unreadable.
func installedVersion() string {
	if _, err := os.Stat(syncthingBin); os.IsNotExist(err) {
		return ""
	}
	cmd := exec.Command(syncthingBin, "--version")
	cmd.Env = append(os.Environ(), "HOME=/data/local/tmp") // fake HOME, else syncthing aborts
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if m := versionRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

var versionRe = regexp.MustCompile(`syncthing v([0-9][^ ]*)`)

// syncArchFor maps a Go GOARCH to syncthing's release naming; unknown
// architectures pass through unchanged (syncthing supports them all, and the
// release file simply may not exist for niche ones).
func syncArchFor(goarch string) string {
	switch goarch {
	case "arm64":
		return "arm64"
	case "amd64":
		return "amd64"
	case "arm":
		return "arm"
	case "386":
		return "386"
	}
	return goarch
}

// syncArch uses the web server's own build architecture (build.sh
// cross-compiles with GOARCH, so they always match).
func syncArch() string {
	return syncArchFor(runtime.GOARCH)
}

// downloadBinary fetches the release tarball for version, trying the direct
// GitHub URL first and the ghfast.top proxy as a fallback (GitHub is
// unreachable on some networks). Returns the tarball path; the caller removes
// it. Per-attempt errors are accumulated into the returned error.
func downloadBinary(ctx context.Context, version string) (string, error) {
	filename := fmt.Sprintf("syncthing-linux-%s-v%s.tar.gz", syncArch(), version)
	urls := []struct{ label, url string }{
		{"direct", "https://github.com/syncthing/syncthing/releases/download/v" + version + "/" + filename},
		{"ghfast.top", "https://ghfast.top/https://github.com/syncthing/syncthing/releases/download/v" + version + "/" + filename},
	}

	f, err := os.CreateTemp("", "syncthing_update_*.tar.gz")
	if err != nil {
		return "", err
	}
	// clean up the temp file on failure only — on success the caller owns it
	kept := false
	defer func() {
		f.Close()
		if !kept {
			os.Remove(f.Name())
		}
	}()

	var notes []string
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.url, nil)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", u.label, err))
			continue
		}
		req.Header.Set("User-Agent", "syncthing4root")
		client := &http.Client{Timeout: 5 * time.Minute, Transport: updateTransport}
		resp, err := client.Do(req)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", u.label, err))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			notes = append(notes, fmt.Sprintf("%s: HTTP %d: %.100s", u.label, resp.StatusCode, body))
			continue
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			resp.Body.Close()
			notes = append(notes, fmt.Sprintf("%s: %v", u.label, err))
			continue
		}
		resp.Body.Close()
		if err := f.Close(); err != nil {
			return "", err
		}
		kept = true
		return f.Name(), nil
	}
	return "", fmt.Errorf("download failed (%s): %s", filename, strings.Join(notes, "; "))
}

// installBinary extracts the syncthing binary from tarball and atomically
// replaces syncthingBin with it (write to a temp file in the same directory,
// then rename — a crash mid-copy can never leave a truncated binary).
func installBinary(tarball string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("no syncthing binary found in %s", tarball)
		}
		if err != nil {
			return err
		}
		// the real binary sits at the top level as
		// syncthing-linux-<arch>-v<ver>/syncthing — etc/freebsd-rc/syncthing
		// etc. are unrelated scripts with the same basename
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "syncthing" {
			continue
		}
		if strings.Count(hdr.Name, "/") != 1 || !strings.HasPrefix(hdr.Name, "syncthing-linux-") {
			continue
		}
		tmp, err := os.CreateTemp(syncthingDir, ".syncthing_new_")
		if err != nil {
			return err
		}
		abort := func(err error) error {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
		if _, err := io.Copy(tmp, tr); err != nil {
			return abort(err)
		}
		if err := tmp.Chmod(0755); err != nil {
			return abort(err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		_ = os.Chown(tmp.Name(), 0, 3005) // syncthing runs as media_rw; fails harmlessly off-device
		if err := os.Rename(tmp.Name(), syncthingBin); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		return nil
	}
}

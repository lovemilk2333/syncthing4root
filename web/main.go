package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

//go:embed frontend/dist
var frontendFS embed.FS

var (
	moduleDir     string
	syncthingDir  string
	syncthingBin  string
	syncthingHome string
	autostartFlag string
	pidFile       string
	authUser      string
	authPassHash  string // bcrypt hash of the password
	useTLS        = true
	startMu       sync.Mutex // serialize start/stop/update so we never launch twice
)

// ── helpers ─────────────────────────────────────────────────────────────

func getSyncthingPID() (int, error) {
	// try PID lock file first
	pid, err := readPidFile()
	if err == nil {
		return pid, nil
	}

	// pidfile stale or missing — rescan /proc (covers manual restart, boot without pidfile)
	pid, err = scanProcForBinary(syncthingBin)
	if err != nil {
		return 0, err
	}

	// update pidfile so next check is instant
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0644)
	return pid, nil
}

func readPidFile() (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	// verify via /proc/pid/exe symlink (more reliable than cmdline)
	exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return 0, err
	}
	if exePath != syncthingBin {
		return 0, fmt.Errorf("pid %d is not syncthing", pid)
	}
	return pid, nil
}

func scanProcForBinary(binPath string) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		// follow /proc/pid/exe symlink for reliable binary identification
		exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			continue
		}
		if exePath == binPath {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("not found")
}

func isRunning() bool {
	_, err := getSyncthingPID()
	return err == nil
}

func getSyncthingURL() string {
	// config.xml is tiny and may change between reads (port/TLS toggled in the
	// Syncthing UI), so read it fresh each time rather than caching a stale value.
	return readSyncthingURLFromConfig()
}

func readSyncthingURLFromConfig() string {
	configPath := filepath.Join(syncthingHome, "config.xml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "http://127.0.0.1:8384/"
	}
	content := string(data)
	addr := "127.0.0.1:8384"
	tls := false

	if idx := strings.Index(content, "<gui"); idx >= 0 {
		section := content[idx:]
		if end := strings.Index(section, "</gui>"); end >= 0 {
			section = section[:end]
		}
		if strings.Contains(section, `tls="true"`) {
			tls = true
		}
		if a := strings.Index(section, "<address>"); a >= 0 {
			sub := section[a+9:]
			if e := strings.Index(sub, "</address>"); e >= 0 {
				addr = sub[:e]
			}
		}
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1" + addr[len("0.0.0.0"):]
	}
	protocol := "http"
	if tls {
		protocol = "https"
	}
	return fmt.Sprintf("%s://%s/", protocol, addr)
}

// ── TLS ─────────────────────────────────────────────────────────────────

func loadOrGenerateTLS() (certFile, keyFile string, err error) {
	certFile = filepath.Join(syncthingDir, "tls.crt")
	keyFile = filepath.Join(syncthingDir, "tls.key")

	if _, err := os.Stat(certFile); err == nil {
		return certFile, keyFile, nil
	}

	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", fmt.Errorf("generate RSA key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "syncthing4root"},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		return "", "", err
	}

	return certFile, keyFile, nil
}

// ── auth ────────────────────────────────────────────────────────────────

func authConfigPath() string {
	return filepath.Join(syncthingDir, ".auth_config")
}

// hashPassword produces a bcrypt hash (same scheme as `caddy hash-password`).
func hashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// looksHashed reports whether v is already a bcrypt hash rather than plaintext.
func looksHashed(v string) bool {
	return strings.HasPrefix(v, "$2a$") || strings.HasPrefix(v, "$2b$") || strings.HasPrefix(v, "$2y$")
}

func saveAuthConfig() {
	_ = os.WriteFile(authConfigPath(),
		[]byte("username="+authUser+"\npassword="+authPassHash+"\n"), 0600)
}

func loadOrGenerateAuthCredentials() {
	cfgFile := authConfigPath()

	// try reading existing config
	if data, err := os.ReadFile(cfgFile); err == nil {
		storedPass := ""
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "username=") {
				authUser = strings.TrimPrefix(line, "username=")
			} else if strings.HasPrefix(line, "password=") {
				storedPass = strings.TrimPrefix(line, "password=")
			}
		}
		if authUser != "" && storedPass != "" {
			if looksHashed(storedPass) {
				authPassHash = storedPass
			} else {
				// migrate legacy plaintext password to a bcrypt hash on disk
				if h, err := hashPassword(storedPass); err == nil {
					authPassHash = h
					saveAuthConfig()
				}
			}
			if authPassHash != "" {
				return
			}
		}
	}

	// default credentials for first run
	authUser = "admin"
	if h, err := hashPassword("admin"); err == nil {
		authPassHash = h
	}
	saveAuthConfig()
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, pass, ok := c.Request.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(authUser)) == 1
		passOK := bcrypt.CompareHashAndPassword([]byte(authPassHash), []byte(pass)) == nil
		if !ok || !userOK || !passOK {
			c.Header("WWW-Authenticate", `Basic realm="syncthing4root"`)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

// ── UI handler (injects auth token into HTML) ─────────────────────────

func serveUI(c *gin.Context) {
	path := c.Param("filepath")
	if path == "" || path == "/" {
		path = "/index.html"
	}

	data, err := frontendFS.ReadFile("frontend/dist" + path)
	if err != nil {
		// SPA fallback: unknown paths render the app shell
		data, err = frontendFS.ReadFile("frontend/dist/index.html")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		path = "/index.html"
	}

	if strings.HasPrefix(path, "/assets/") {
		// Vite emits content-hashed asset names — safe to cache forever
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}

	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		c.Data(http.StatusOK, ct, data)
		return
	}
	c.Data(http.StatusOK, http.DetectContentType(data), data)
}

// ── API handlers ────────────────────────────────────────────────────────

func handleStatus(c *gin.Context) {
	running := isRunning()
	resp := gin.H{"running": running}
	if running {
		pid, _ := getSyncthingPID()
		resp["pid"] = pid
	} else {
		resp["pid"] = nil
	}
	resp["url"] = getSyncthingURL()
	resp["username"] = authUser
	c.JSON(http.StatusOK, resp)
}

// userStorageHome mirrors service.sh: pick the first /storage/emulated/* user,
// falling back to /storage/emulated/0. Used only for the `~` path.
func userStorageHome() string {
	if entries, err := os.ReadDir("/storage/emulated"); err == nil {
		for _, e := range entries {
			return filepath.Join("/storage/emulated", e.Name())
		}
	}
	return "/storage/emulated/0"
}

// errAlreadyRunning / errNotRunning let callers distinguish no-op cases.
var (
	errAlreadyRunning = fmt.Errorf("syncthing is already running")
	errNotRunning     = fmt.Errorf("syncthing is not running")
	errBinNotFound    = fmt.Errorf("syncthing binary not found")
)

// startSyncthing launches Syncthing if not already running and returns its PID.
// reason is recorded in the state-change log. Shared by the HTTP handler and
// the network-policy monitor; acquires startMu so a double-tap or a policy
// tick can't race the DB lock.
func startSyncthing(reason string) (int, error) {
	startMu.Lock()
	defer startMu.Unlock()
	return startSyncthingLocked(reason)
}

// startSyncthingLocked assumes startMu is already held.
func startSyncthingLocked(reason string) (int, error) {
	if isRunning() {
		return 0, errAlreadyRunning
	}
	if _, err := os.Stat(syncthingBin); os.IsNotExist(err) {
		return 0, errBinNotFound
	}
	cmd := exec.Command(syncthingBin, "serve", "--home="+syncthingHome, "--no-browser")
	cmd.Stdout = syncthingLog
	cmd.Stderr = syncthingLog
	cmd.Env = append(os.Environ(), "HOME="+userStorageHome())

	if err := cmd.Start(); err != nil {
		recordState("start failed", reason+" ("+err.Error()+")")
		return 0, err
	}
	pid := cmd.Process.Pid
	recordState(fmt.Sprintf("started (pid %d)", pid), reason)
	syncthingLog.Write([]byte(fmt.Sprintf("=== syncthing started (pid %d) ===\n", pid)))
	// reap the child when it exits so it never lingers as a <defunct> zombie
	// (the web server is a long-lived parent) and push the new state to the UI.
	go func() {
		err := cmd.Wait()
		syncthingLog.flushPartial()
		syncthingLog.Write([]byte(fmt.Sprintf("=== syncthing exited: %v ===\n", err)))
		if err != nil {
			recordState("exited", "process ended: "+err.Error())
		} else {
			recordState("exited", "process ended cleanly")
		}
		signal(statusEventCh)
		publishSnapshot()
	}()
	// save PID to lock file
	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0644)
	return pid, nil
}

// stopSyncthing gracefully stops Syncthing (SIGINT, then SIGKILL fallback).
// reason is recorded in the state-change log; acquires startMu.
func stopSyncthing(reason string) error {
	startMu.Lock()
	defer startMu.Unlock()
	return stopSyncthingLocked(reason)
}

// stopSyncthingLocked assumes startMu is already held.
func stopSyncthingLocked(reason string) error {
	pid, err := getSyncthingPID()
	if err != nil {
		return errNotRunning
	}

	// graceful shutdown — SIGINT lets Syncthing flush DB and notify peers
	exec.Command("kill", "-2", strconv.Itoa(pid)).Run()

	// wait for graceful exit, force if still alive
	time.Sleep(2 * time.Second)
	forced := false
	if _, err := getSyncthingPID(); err == nil {
		exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
		forced = true
	}

	// clean up pid file
	os.Remove(pidFile)
	if forced {
		recordState(fmt.Sprintf("stopped (pid %d, forced SIGKILL)", pid), reason)
	} else {
		recordState(fmt.Sprintf("stopped (pid %d)", pid), reason)
	}
	signal(statusEventCh)
	return nil
}

// autostartSyncthing launches Syncthing in the background on boot when the
// autostart toggle is enabled. The web server owns the process so its entire
// output can be captured into the in-memory log buffer instead of a file.
func autostartSyncthing() {
	if _, err := os.Stat(autostartFlag); err == nil {
		return // autostart disabled by the user
	}
	if _, err := os.Stat(syncthingBin); err != nil {
		return
	}
	if isRunning() {
		return
	}

	if _, err := startSyncthing("autostart at boot"); err != nil && err != errAlreadyRunning {
		fmt.Fprintf(os.Stderr, "syncthing autostart failed: %v\n", err)
	}
	publishSnapshot()
}

func handleStart(c *gin.Context) {
	pid, err := startSyncthing("user request (web UI: Start)")
	switch {
	case err == errAlreadyRunning:
		c.JSON(http.StatusConflict, gin.H{"error": "Syncthing is already running"})
	case err == errBinNotFound:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Syncthing binary not found"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "Syncthing started", "pid": pid})
		publishSnapshot()
	}
}

func handleStop(c *gin.Context) {
	if err := stopSyncthing("user request (web UI: Stop)"); err == errNotRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "Syncthing is not running"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Syncthing stopped"})
	publishSnapshot()
}

func handleOpenSyncthing(c *gin.Context) {
	url := getSyncthingURL()
	cmd := exec.Command("am", "start", "-a", "android.intent.action.VIEW", "-d", url)
	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "message": "Opening Syncthing UI..."})
}

func handleAutostartStatus(c *gin.Context) {
	_, err := os.Stat(autostartFlag)
	disabled := err == nil
	c.JSON(http.StatusOK, gin.H{
		"autostart": !disabled,
		"disabled":  disabled,
	})
}

func handleAutostartDisable(c *gin.Context) {
	f, err := os.Create(autostartFlag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	f.Close()
	c.JSON(http.StatusOK, gin.H{"message": "Autostart disabled"})
	publishSnapshot()
}

func handleAutostartEnable(c *gin.Context) {
	if err := os.Remove(autostartFlag); err != nil && !os.IsNotExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Autostart enabled"})
	publishSnapshot()
}

func handleSyncthingURL(c *gin.Context) {
	url := getSyncthingURL()
	c.JSON(http.StatusOK, gin.H{"url": url})
}

func handleChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.NewPassword == "" || len(req.NewPassword) < 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 4 characters"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(authPassHash), []byte(req.OldPassword)) != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "current password is incorrect"})
		return
	}

	newHash, err := hashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}
	authPassHash = newHash
	saveAuthConfig()
	c.JSON(http.StatusOK, gin.H{"message": "password updated"})
}

func handleChangeUsername(c *gin.Context) {
	var req struct {
		Password    string `json:"password"`
		NewUsername string `json:"new_username"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.NewUsername == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username cannot be empty"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(authPassHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "password is incorrect"})
		return
	}

	authUser = req.NewUsername
	saveAuthConfig()
	c.JSON(http.StatusOK, gin.H{"message": "username updated"})
	publishSnapshot()
}

// handleAccountUpdate updates the username and/or password in one request.
// Either field may be omitted, the current password is always required.
func handleAccountUpdate(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewUsername     string `json:"new_username"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(authPassHash), []byte(req.CurrentPassword)) != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "current password is incorrect"})
		return
	}

	newUser := strings.TrimSpace(req.NewUsername)
	newPass := req.NewPassword
	if newUser == "" && newPass == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nothing to update"})
		return
	}
	if newPass != "" && len(newPass) < 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 4 characters"})
		return
	}

	var newHash string
	if newPass != "" {
		h, err := hashPassword(newPass)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
			return
		}
		newHash = h
	}

	// apply only after every check passed so a rejected request changes nothing
	if newUser != "" {
		authUser = newUser
	}
	if newHash != "" {
		authPassHash = newHash
	}
	saveAuthConfig()
	c.JSON(http.StatusOK, gin.H{"message": "account updated"})
	publishSnapshot()
}

func handleDNSConfigGet(c *gin.Context) {
	cfg := dnsCfg.Load().(dnsConfig)
	c.JSON(http.StatusOK, gin.H{
		"dns": cfg.Server,
	})
}

func handleDNSConfigSave(c *gin.Context) {
	var req struct {
		DNS string `json:"dns"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := saveDNSConfig(req.DNS); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "DNS config saved, applied to the next request"})
}

func handleUpdate(c *gin.Context) {
	startMu.Lock()
	defer startMu.Unlock()

	// bound check + install so a stalled network can't hang the request forever
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	defer publishSnapshot()

	// Phase 1 — check only. Syncthing is left untouched until we know there
	// is an actual update, so a failed or unnecessary check never stops it.
	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	installed := installedVersion()
	if installed == latest {
		c.JSON(http.StatusOK, gin.H{"message": "already up to date (v" + latest + ")"})
		return
	}

	// Phase 2 — update available. Stop syncthing to free the binary, download,
	// install, then restore the previous running state.
	wasRunning := isRunning()
	updateReason := "updating Syncthing " + orNone(installed) + " -> v" + latest
	if wasRunning {
		_ = stopSyncthingLocked(updateReason)
	}
	msg := updateReason + "\n"
	tarball, err := downloadBinary(ctx, latest)
	if err == nil {
		err = installBinary(tarball)
	}
	os.Remove(tarball) // no-op when the download failed

	restartNote := ""
	if wasRunning {
		if _, rerr := startSyncthingLocked("restart after update to v" + latest); rerr != nil && rerr != errAlreadyRunning {
			restartNote = "\n(warning: failed to restart syncthing: " + rerr.Error() + ")"
		}
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "update timed out"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": msg + err.Error() + restartNote})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": msg + "updated to syncthing v" + latest + restartNote})
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return "v" + v
}

// ── main ────────────────────────────────────────────────────────────────

func main() {
	captureProcessOutput()

	port := "48344"
	moduleDir = ""
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
		if arg == "--module-dir" && i+1 < len(os.Args) {
			moduleDir = os.Args[i+1]
		}
		if arg == "--no-tls" {
			useTLS = false
		}
	}

	if moduleDir == "" {
		execPath, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get executable path: %v\n", err)
			os.Exit(1)
		}
		moduleDir = filepath.Dir(execPath)
	}

	syncthingDir = filepath.Join(moduleDir, "syncthing")
	syncthingBin = filepath.Join(syncthingDir, "syncthing")
	syncthingHome = filepath.Join(syncthingDir, "home")
	autostartFlag = filepath.Join(syncthingDir, ".autostart_disabled")
	pidFile = filepath.Join(syncthingDir, "syncthing.pid")

	// Static Linux binaries running on Android cannot use /etc/resolv.conf.
	// Install the dnsproxyd-backed resolver before any update request is made.
	maybeInstallAndroidDNSResolver()

	// Logs are memory-only now; remove the old file-based log so an upgraded
	// module does not keep a stale service.log on flash.
	_ = os.Remove(filepath.Join(syncthingDir, "service.log"))

	loadOrGenerateAuthCredentials()
	loadDNSConfig()
	loadLogsConfig()
	loadNetPolicy()
	startEventSources()
	startNetPolicyMonitor()
	go autostartSyncthing()

	gin.DefaultWriter = io.Discard
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// global auth — browser pops login dialog for every page
	r.Use(authMiddleware())

	// redirect / -> /ui/
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/ui/")
	})

	// UI (auth handled by browser, no injection needed)
	r.GET("/ui/*filepath", serveUI)

	// API
	api := r.Group("/api")
	{
		api.GET("/status", handleStatus)
		api.GET("/events", handleEvents)
		api.POST("/start", handleStart)
		api.POST("/stop", handleStop)
		api.GET("/syncthing-url", handleSyncthingURL)
		api.POST("/open-syncthing", handleOpenSyncthing)
		api.GET("/autostart", handleAutostartStatus)
		api.POST("/autostart/disable", handleAutostartDisable)
		api.POST("/autostart/enable", handleAutostartEnable)
		api.POST("/change-password", handleChangePassword)
		api.POST("/change-username", handleChangeUsername)
		api.POST("/account", handleAccountUpdate)
		api.POST("/update", handleUpdate)
		api.GET("/dns-config", handleDNSConfigGet)
		api.POST("/dns-config", handleDNSConfigSave)
		api.GET("/netpolicy", handleNetPolicyGet)
		api.POST("/netpolicy", handleNetPolicySave)
		api.GET("/netpolicy/scan", handleNetPolicyScan)
		api.GET("/logs", handleLogsList)
		api.GET("/logs/stream", handleLogStream)
		api.POST("/logs/config", handleLogsConfig)
	}

	addr := "127.0.0.1:" + port

	if useTLS {
		certFile, keyFile, err := loadOrGenerateTLS()
		if err != nil {
			fmt.Fprintf(os.Stderr, "TLS setup failed (%v), falling back to HTTP\n", err)
			startServer(r, "http", addr, "", "")
			return
		}
		startServer(r, "https", addr, certFile, keyFile)
	} else {
		startServer(r, "http", addr, "", "")
	}
}

// startServer records the actual scheme for action.sh (so it never assumes
// https when we fell back to http) and runs the server.
func startServer(r *gin.Engine, scheme, addr, certFile, keyFile string) {
	uiURL := fmt.Sprintf("%s://%s/ui/", scheme, addr)
	// persist the real URL so action.sh opens the correct scheme
	_ = os.WriteFile(filepath.Join(syncthingDir, ".webui_url"), []byte(uiURL+"\n"), 0644)

	fmt.Printf("syncthing4root web server -> %s\n-> Login user: %s (password unchanged; default is 'admin' on first run)\n", uiURL, authUser)

	var err error
	if scheme == "https" {
		err = r.RunTLS(addr, certFile, keyFile)
	} else {
		err = r.Run(addr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

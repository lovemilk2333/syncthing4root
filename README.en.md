# syncthing4root

Run Syncthing as root so the system cannot force-stop it. Comes with a **web management UI** and a **web API** (TLS + Basic Auth)

[中文](README.md) | English

## Installation

1. Download the zip from Releases
2. Flash the module in Magisk / KernelSU / APatch
3. Choose a download source when prompted (GitHub direct or ghfast.top mirror)
4. Reboot

## Usage

### Web UI

Tap the module manager's **Action button (>)**: the backend starts automatically and opens in your browser. The UI provides:

| Feature | Description |
|---------|-------------|
| 📂 Open Syncthing UI | Open Syncthing's native web UI |
| ▶ Start | Start the Syncthing daemon |
| ■ Stop | Stop Syncthing |
| Auto-start on boot | Toggle startup at boot |
| 🔑 Account | Change the management login username / password |
| 📜 Logs | Switch between Syncthing output / web server / state-change logs; everything is kept in in-memory ring buffers, never on disk; state changes include an ISO timestamp and the reason; retained lines are configurable (default 128) |
| 🔄 Check & Update Syncthing | Check for and install the latest Syncthing |
| 🌐 DNS Resolver | Configure DNS used by update checks (auto-detected LAN DNS / fixed IP / DoH / DoT); auto mode ignores non-LAN DNS; system DNS is discovered through `github.com/AdguardTeam/golibs/netutil/sysresolv`, then Android-specific probes; fixes DNS failures in Android root processes |

Default address: `https://127.0.0.1:48344/ui/` (default credentials `admin` / `admin`)

> The UI is built with **Vue 3 + PrimeVue 4 (MIT)** and receives state through **SSE (Server-Sent Events)**; the page itself never polls. Network / battery-saver changes are driven by **Android system events** (`logcat -b events` / netlink / inotify) instead of periodically querying battery / WiFi state
>
> PrimeVue 4 is used instead of PrimeVue 5: v5 includes license validation (`#p-license-host` overlay / online check), which is unsuitable for an offline root module; v4 is the MIT version with no online validation

> The self-signed certificate triggers a browser security warning; choose "Advanced / Proceed"
> If your browser cannot handle self-signed certificates, fall back to HTTP with `--no-tls`:
>
> ```sh
> su -c '/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing4root_webserver --port 48344 --module-dir /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root --no-tls'
> ```

### Manual management

- **Data directory**: `/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/` (contains `config.xml`, etc.)
- **Startup service**: `/data/adb/service.d/syncthing_service.sh`
- **Auth config**: `/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/.auth_config` (format: `username=...` / `password=<bcrypt hash>`)
- **TLS certificate**: `/data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/tls.{crt,key}`

### API

All `/api/*` endpoints use HTTP Basic Auth. Default username `admin`, default password `admin` (written to `.auth_config` on first run)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/status` | Syncthing status, PID, GUI address |
| GET | `/api/events` | SSE stream of state / network-policy snapshots |
| POST | `/api/start` | Start Syncthing |
| POST | `/api/stop` | Stop Syncthing |
| GET | `/api/syncthing-url` | Syncthing GUI address |
| POST | `/api/open-syncthing` | Open the Syncthing UI in a browser |
| GET | `/api/autostart` | Autostart toggle state |
| POST | `/api/autostart/enable` | Enable autostart |
| POST | `/api/autostart/disable` | Disable autostart |
| POST | `/api/update` | Update Syncthing to the latest version |
| GET  | `/api/dns-config` | Read DNS resolver config + auto-detected DNS list (each entry marked LAN or not) |
| POST | `/api/dns-config` | Save DNS config (empty = auto / IP / IP:port / DoH `https://` / DoT `tls://`) |
| POST | `/api/account` | Update username and/or password in one request (current password required; empty fields mean unchanged) |
| POST | `/api/change-password` | Change the login password (old password required) |
| POST | `/api/change-username` | Change the login username (password required) |
| GET  | `/api/netpolicy` | Read network policy config + current network state |
| POST | `/api/netpolicy` | Save network policy config (hot reload, re-evaluated immediately) |
| GET  | `/api/netpolicy/scan` | Scan current transport type / WiFi SSID (for whitelist autofill) |
| GET  | `/api/logs` | List in-memory log sources (`state` / `syncthing` / `webserver`) and the retained-line setting |
| GET  | `/api/logs/stream?source=state` | SSE log stream (sends retained in-memory history first) |
| POST | `/api/logs/config` | Set retained lines `{"lines":128}` (16–4000); excess entries are dropped immediately |

### Web server options

```
syncthing4root_webserver --port <port> --module-dir <module dir> [--no-tls]
```

- `--port`: listen port (default `48344`)
- `--module-dir`: module path (defaults to the binary's own directory)
- `--no-tls`: disable TLS (plain HTTP)

### Network policy (automatic start/stop by network state)

The **📶 Syncthing Running Policy** card can start / stop Syncthing based on the current network state. Disabled by default; enable it manually

- **Master switch**: when enabled, the policy controls start/stop; when disabled it is manual mode (the status bar shows an `auto` / `manual` badge)
- **Event-driven detection**: no fixed-interval polling. It consumes **Android events** directly: WiFi / network / battery / power-saver events from `logcat -b events`, together with netlink (`ip monitor`) and inotify (Android settings database). Changes are re-evaluated and pushed to the UI immediately
- **Probe interval**: used only by `probe` (reachability) — reachability is inherently periodic. Without `probe`, the policy is fully event-driven. Default 30s, range 5–3600

**Run condition (boolean expression)**: one expression describing "when Syncthing should run", supporting `AND` / `OR` / `NOT` and arbitrary parentheses nesting

Four terms and what `true` means:

| term | true when | Configuration |
|------|-----------|---------------|
| `wifi` | Connected to a WiFi network whose SSID is whitelisted | WiFi whitelist (with "📡 Scan current SSID") |
| `cellular` | Currently on a cellular (mobile data) network | — |
| `power` | System Battery Saver is enabled | — |
| `probe` | The probe target is reachable | Ping/TCPing + target + timeout + up/down thresholds (hysteresis) |

Examples:

```
wifi AND NOT cellular              # run on whitelisted WiFi, not on cellular
wifi AND (probe OR NOT power)      # whitelisted WiFi and (probe up or not power-saving)
NOT cellular AND NOT power         # run whenever not on cellular and not power-saving
```

- Syntax: keywords are case-insensitive; `&&` / `||` / `!` are accepted as aliases for `AND` / `OR` / `NOT`
- **Empty expression = always run** (no constraints)
- When a term's panel is **disabled**: `wifi` / `cellular` / `probe` are always `true`, while `power` is always `false` (keeping `NOT power` neutral under `AND`). Note that under `NOT` / `OR` these defaults affect the result
- The backend validates syntax and identifiers on save; invalid expressions are rejected with an error
- Probe `up` / `down` thresholds: the state flips only after N consecutive successes / failures (hysteresis, avoids flapping on unstable networks)

> [!NOTE]
> - While enabled, the policy overrides manual Start / Stop (the next evaluation reconciles to the policy). The `auto` badge in the status bar means automatic mode
> - Network / power detection is fully event-driven: Android event log (`logcat -b events`, underlying WiFi / network / battery / power-saver broadcasts) + netlink (`ip monitor`) + inotify (`settings_global.xml`). `ip route` / `cmd wifi status` / `settings get global low_power` run only when an event arrives; battery / WiFi state is never polled. No Frida/Xposed hooks
> - The UI receives state over SSE (`/api/events`) and never polls. The status card shows which Android event sources are available (`logcat` / `netlink` / `inotify`)
> - `probe` (reachability) is the only periodic check — network reachability cannot be observed through system events. Without `probe`, there is no periodic detection at all
> - If Android system commands are unavailable or SSID detection fails, `wifi` evaluates to `false`
> - Config is persisted in `syncthing/.netpolicy.json`; legacy (beta3/4) `combine` configs are migrated to an equivalent expression on first load

## Migrating from the Syncthing Android App

If you previously used `com.nutomic.syncthingandroid`, you can migrate the existing config:

1. After installing the module, disconnect WiFi and other links so other Syncthing instances on the LAN don't discover the device after reboot
2. Reboot
3. Open Syncthing, tap the gear icon in the top-right > "Shutdown", or run `su -c 'pkill syncthing'`
4. Copy the config:
    ```sh
    # copy config into the syncthing home directory
    cp -r /data/data/com.nutomic.syncthingandroid/files/* /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/

    # fix directory and file permissions
    chmod 0755 /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home
    chmod 0644 /data/adb/modules/io.github.lovemilk2333.root_module.syncthing4root/syncthing/home/*
    ```

> [!WARNING]
> If your Syncthing did not run as root, the folder paths in `config.xml` are likely rooted at **internal storage**, which causes path mismatches
>
> To keep your data safe, verify and migrate them to real paths, e.g. `/sync` -> `/storage/emulated/0/sync`
> **The author and everyone involved in this module accept no responsibility for data loss caused by unbacked-up Syncthing config and/or synced data.**

5. After migrating, reboot or tap **Start** in the UI
   (at boot the backend starts Syncthing according to the autostart toggle; its output goes to the in-memory log)
6. Restore internet connectivity

## Security

- **TLS**: a 4096-bit RSA self-signed certificate is generated on first start (valid for 10 years); all traffic uses HTTPS
- **Auth**: HTTP Basic Auth, default username `admin`, default password `admin` (written to `.auth_config` on first run)
- **Password storage**: passwords in `.auth_config` are stored as bcrypt hashes (same algorithm as `caddy hash-password`); no plaintext is kept
- **Autostart toggle**: disabling autostart creates an `.autostart_disabled` flag in the data directory; the backend skips starting Syncthing when it sees the file
- **Logs**: Syncthing output, backend output and state-change records are written only to in-memory ring buffers (128 lines each by default); no log files are created. State-change entries use ISO-8601 timestamps (with numeric offsets such as `+08:00`) and include the reason

> [!WARNING]
> **Downgrade note**: starting with this version, the password field in `.auth_config` is a bcrypt hash instead of plaintext. On first start after upgrading, the old plaintext password is rewritten as a hash. This is **irreversible**
> If you later downgrade to an older version (which compares plaintext passwords), the original password will no longer work. Before downgrading, edit `password=` in `.auth_config` back to plaintext, or delete the file so the module regenerates the default `admin` / `admin`

## Packaging

```sh
sh build.sh
```

Build steps:
1. Build the frontend `web/frontend` (Vue 3 + PrimeVue + Vite) -> `web/frontend/dist`
2. Cross-compile the Go web server -> `build/syncthing4root_webserver` (ARM64, frontend embedded via `go:embed`)
3. Copy module files into `build/`
4. Package `build/` into a zip -> `release/<id>-<version>.zip`

### Development environment

```sh
# frontend (Vue 3 + PrimeVue)
node -v            # Node.js 20+
cd web/frontend
npm install
npm run dev        # local dev (reverse-proxy the API to the backend yourself)
npm run build      # produces dist/, embedded by Go

# Go cross-compilation toolchain (Go 1.27+)
# ARM64 cross-compilation works out of the box
go version

# build (build.sh runs this automatically)
cd web && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ../build/syncthing4root_webserver .
```

### `versionCode` rules
`versionCode` consists of an 8-digit date, 1 alpha/beta flag and a 3-digit sequence number. For multiple releases on the same day the sequence increments by 1, e.g. `202607070001` is the first stable release on 2026-07-07

For alpha/beta builds, the date is the next stable release date minus 1 day, the alpha/beta flag is 1, followed by the sequence number. For example `202607151001` is the first alpha/beta build for 2026-07-16, `202607151002` the second, and `202607160001` the first stable release on 2026-07-16

## License

The module configuration and packaging scripts are licensed under BSD 3-Clause; see [LICENSE](LICENSE) for details
Syncthing itself is licensed under MPL-2.0, see https://github.com/syncthing/syncthing

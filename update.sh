#!/system/bin/sh
# syncthing update script — called by syncthing4root_webserver API

set -e

MODDIR="$1"
[ -z "$MODDIR" ] && echo "usage: $0 <module_dir>" && exit 1

syncthing_dir="$MODDIR/syncthing"
syncthing_bin="$syncthing_dir/syncthing"

# ── detect architecture ──
arch=$(uname -m)
case "$arch" in
  aarch64|arm64) sync_arch="arm64" ;;
  x86_64|amd64)  sync_arch="amd64" ;;
  armv7*|arm|armv8l) sync_arch="arm" ;;
  i686|x86|i386) sync_arch="386" ;;
  *) echo "unsupported arch: $arch"; exit 1 ;;
esac

# ── detect download tool ──
dl() {
  local url="$1" out="$2"
  if command -v curl >/dev/null 2>&1; then
    [ -n "$out" ] && curl -L -o "$out" "$url" || curl -s "$url"
  else
    [ -n "$out" ] && wget -O "$out" "$url" 2>/dev/null || wget -q -O - "$url" 2>/dev/null
  fi
}

# ── get installed version ──
installed_ver=""
if [ -f "$syncthing_bin" ]; then
  installed_ver=$(HOME=/data/local/tmp "$syncthing_bin" --version 2>/dev/null | grep -o 'syncthing v[^ ]*' | sed 's/syncthing v//')
fi

# ── fetch latest version from GitHub API ──
json=$(dl "https://api.github.com/repos/syncthing/syncthing/releases/latest" "")
latest=$(echo "$json" | grep '"tag_name"' | sed 's/.*"v//;s/".*//')

# fallback to upgrades mirror
if [ -z "$latest" ]; then
  json=$(dl "https://upgrades.syncthing.net/meta.json" "")
  latest=$(echo "$json" | tr -d '\n\r ' | sed 's/},{"tag_name"/\n{"tag_name"/g' | grep '"prerelease":false' | head -1 | sed 's/.*"tag_name":"v//;s/".*//')
fi

[ -z "$latest" ] && echo "failed to get latest version" && exit 1

echo "current: ${installed_ver:-none}  latest: $latest"

[ "$installed_ver" = "$latest" ] && echo "already up to date" && exit 0

# ── download ──
filename="syncthing-linux-${sync_arch}-v${latest}.tar.gz"
tmpdir="/data/local/tmp/syncthing_update"
mkdir -p "$tmpdir"
rm -rf "${tmpdir:?}"/*

echo "downloading $filename..."
dl "https://github.com/syncthing/syncthing/releases/download/v${latest}/${filename}" "$tmpdir/$filename"

extract_dir="syncthing-linux-${sync_arch}-v${latest}"
tar -xzf "$tmpdir/$filename" -C "$tmpdir" "${extract_dir}/syncthing"

# ── install ──
cp "$tmpdir/${extract_dir}/syncthing" "$syncthing_bin"
chmod 755 "$syncthing_bin"
chown 0:3005 "$syncthing_bin" 2>/dev/null || true

rm -rf "${tmpdir:?}"
echo "updated to syncthing $latest"

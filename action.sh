#!/system/bin/sh

MODDIR=${0%/*}
PORT=${1:-48344}

# prefer the scheme the web server actually bound to (it may have fallen back to
# http if TLS setup failed); fall back to https if the marker file is missing.
url_file="$MODDIR/syncthing/.webui_url"
if [ -f "$url_file" ]; then
  url="$(cat "$url_file")"
else
  url="https://127.0.0.1:${PORT}/ui/"
fi

if command -v am >/dev/null 2>&1; then
  am start -a android.intent.action.VIEW -d "$url" >/dev/null 2>&1
else
  echo "am not available, please open $url in your browser"
fi

#!/system/bin/sh

MODDIR=${0%/*}
PORT=${1:-48344}

# open management UI in browser (syncthing4root_webserver auto-starts on boot via service.sh)
url="https://127.0.0.1:${PORT}/ui/"
if command -v am >/dev/null 2>&1; then
  am start -a android.intent.action.VIEW -d "$url" >/dev/null 2>&1
else
  echo "am not available, please open $url in your browser"
fi

#!/system/bin/sh

MODDIR=${0%/*}
syncthing_dir="$MODDIR/syncthing"
config_xml="$syncthing_dir/home/config.xml"

address="127.0.0.1:8384"
protocol="http"

if [ -f "$config_xml" ]; then
  parsed_address=$(grep -A50 '<gui' "$config_xml" | grep '<address>' | sed 's/.*<address>//;s/<\/address>.*//' | head -1)
  [ -n "$parsed_address" ] && address="$parsed_address"

  parsed_tls=$(grep -A50 '<gui' "$config_xml" | grep -o 'tls="[^"]*"' | sed 's/tls="//;s/"//' | head -1)
  [ "$parsed_tls" = "true" ] && protocol="https"
fi

# normalize 0.0.0.0 to localhost for browser
case "$address" in
  0.0.0.0:*)
    address="127.0.0.1${address#0.0.0.0}"
    ;;
esac

url="${protocol}://${address}/"

echo "opening $url"
if command -v am >/dev/null 2>&1; then
  am start -a android.intent.action.VIEW -d "$url" >/dev/null 2>&1
else
  echo "am not available, please open $url in your browser"
fi

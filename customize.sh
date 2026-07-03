#!/system/bin/sh

SKIPUNZIP=1
SKIPMOUNT=false
PROPFILE=true
POSTFSDATA=false
LATESTARTSERVICE=true

if [ "$BOOTMODE" != true ]; then
  abort "-----------------------------------------------------------"
  ui_print "! please install in Magisk/KernelSU/APatch Manager"
  ui_print "! install from recovery is NOT supported"
  abort "-----------------------------------------------------------"
fi

if [ "${KSU:-false}" = true ] && [ "${KSU_VER_CODE:-0}" -lt 10670 ]; then
  abort "-----------------------------------------------------------"
  ui_print "! please update your KernelSU and KernelSU Manager"
  abort "-----------------------------------------------------------"
fi

service_dir="/data/adb/service.d"
if [ "${KSU:-false}" = "true" ]; then
  ui_print "— KernelSU version: ${KSU_VER:-unknown} (${KSU_VER_CODE:-0})"
  [ "${KSU_VER_CODE:-0}" -lt 10683 ] && service_dir="/data/adb/ksu/service.d"
elif [ "${APATCH:-false}" = "true" ]; then
  APATCH_VER=$(cat "/data/adb/ap/version" 2>/dev/null || echo "unknown")
  ui_print "— APatch version: $APATCH_VER"
else
  ui_print "— Magisk version: ${MAGISK_VER:-unknown} (${MAGISK_VER_CODE:-0})"
fi

mkdir -p "$service_dir"

# extract module files first
ui_print "— extracting module files..."
unzip -o "$ZIPFILE" -x 'META-INF/*' -x 'webroot/*' -d "$MODPATH" >&2

# remove dev-only files not needed in module
rm -f "$MODPATH/build.sh" "$MODPATH/.gitignore"

# extract uninstall.sh to module
unzip -j -o "$ZIPFILE" 'uninstall.sh' -d "$MODPATH" >&2

# syncthing data directory inside module
syncthing_data_dir="$MODPATH/syncthing"

# get module id for runtime paths
MODID=$(grep '^id=' "$MODPATH/module.prop" | sed 's/id=//')
runtime_dir="/data/adb/modules/${MODID}/syncthing"

# check if this is an update (home dir exists on old module path)
if [ -d "$runtime_dir/home" ]; then
  ui_print "— updating existing syncthing installation, preserving config"
  mkdir -p "$syncthing_data_dir"
  # copy existing config to staging so service/webroot generation can use it
  cp -r "$runtime_dir/home" "$syncthing_data_dir/"
else
  mkdir -p "$syncthing_data_dir"
fi

# detect architecture
arch=$(uname -m)
case "$arch" in
  aarch64|arm64)
    sync_arch="arm64"
    ;;
  x86_64|amd64)
    sync_arch="amd64"
    ;;
  armv7*|arm|armv8l)
    sync_arch="arm"
    ;;
  i686|x86|i386)
    sync_arch="386"
    ;;
  *)
    abort "unsupported architecture: $arch"
    ;;
esac
ui_print "— architecture: $sync_arch"

# verify curl is available
if ! curl --version >/dev/null 2>&1; then
  abort "curl is required but not found in this environment"
fi

# fetch latest version from GitHub API
ui_print "— fetching latest version from GitHub..."
latest_version=$(curl -s "https://api.github.com/repos/syncthing/syncthing/releases/latest" | grep '"tag_name"' | sed 's/.*"v//;s/".*//')
if [ -z "$latest_version" ]; then
  abort "failed to get latest version, check your network connection"
fi
ui_print "— latest version: $latest_version"

# check if already installed and up to date
skip_download=false
if [ -f "$runtime_dir/syncthing" ]; then
  installed_ver=$("$runtime_dir/syncthing" --version 2>/dev/null | grep -o 'syncthing v[^ ]*' | sed 's/syncthing v//')
  if [ -n "$installed_ver" ] && [ "$installed_ver" = "$latest_version" ]; then
    ui_print "— syncthing $installed_ver already installed, skipping download"
    mkdir -p "$syncthing_data_dir"
    cp "$runtime_dir/syncthing" "$syncthing_data_dir/syncthing"
    if [ -d "$runtime_dir/home" ]; then
      cp -r "$runtime_dir/home" "$syncthing_data_dir/"
    fi
    skip_download=true
  else
    ui_print "— installed: ${installed_ver:-none}, latest: $latest_version, downloading"
  fi
fi

# construct filenames and URLs
filename="syncthing-linux-${sync_arch}-v${latest_version}.tar.gz"
github_url="https://github.com/syncthing/syncthing/releases/download/v${latest_version}/${filename}"
proxy_url="https://ghfast.top/${github_url}"

if [ "$skip_download" != true ]; then
  # ask user about CDN mirror via volume keys
  if command -v getevent >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
    ui_print "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    ui_print "— use ghfast.top proxy to accelerate downloads?"
    ui_print "— [ Vol UP(+): ghfast.top ]"
    ui_print "— [ Vol DOWN(-): GitHub ]"
    START_TIME=$(date +%s)
    use_proxy=""
    while true; do
      NOW_TIME=$(date +%s)
      timeout 1 getevent -lc 1 2>&1 | grep KEY_VOLUME > "$TMPDIR/events" || true
      if [ $(( NOW_TIME - START_TIME )) -gt 9 ]; then
        ui_print "— no input detected after 10 seconds, using GitHub"
        use_proxy="false"
        break
      elif grep -q KEY_VOLUMEUP "$TMPDIR/events"; then
        ui_print "— using ghfast.top"
        use_proxy="true"
        break
      elif grep -q KEY_VOLUMEDOWN "$TMPDIR/events"; then
        ui_print "— using GitHub"
        use_proxy="false"
        break
      fi
    done
    timeout 1 getevent -cl >/dev/null || true
  else
    ui_print "— getevent unavailable, skipping proxy prompt, using GitHub"
    use_proxy="false"
  fi

  # set download URL
  if [ "$use_proxy" = "true" ]; then
    download_url="$proxy_url"
  else
    download_url="$github_url"
  fi

  # prepare temp directory
  syncthing_tmpdir="$TMPDIR/syncthing_download"
  mkdir -p "$syncthing_tmpdir"
  rm -rf "${syncthing_tmpdir:?}"/*

  # download
  ui_print "— downloading $filename..."
  if ! curl -L -o "$syncthing_tmpdir/$filename" "$download_url"; then
    if [ "$use_proxy" = "true" ]; then
      ui_print "! ghfast.top failed, retrying with GitHub..."
      if ! curl -L -o "$syncthing_tmpdir/$filename" "$github_url"; then
        rm -rf "$syncthing_tmpdir"
        abort "download failed, check your network connection"
      fi
    else
      rm -rf "$syncthing_tmpdir"
      abort "download failed, check your network connection"
    fi
  fi

  # extract syncthing binary from tar.gz
  ui_print "— extracting..."
  extract_dir="syncthing-linux-${sync_arch}-v${latest_version}"
  if ! tar -xzf "$syncthing_tmpdir/$filename" -C "$syncthing_tmpdir" "${extract_dir}/syncthing"; then
    rm -rf "$syncthing_tmpdir"
    abort "extraction failed, the file may be corrupted"
  fi

  # copy binary to module
  mkdir -p "$syncthing_data_dir"
  cp "$syncthing_tmpdir/${extract_dir}/syncthing" "$syncthing_data_dir/syncthing"
  rm -rf "$syncthing_tmpdir"
fi

# set permissions
ui_print "— setting permissions..."
set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm_recursive "$syncthing_data_dir" 0 3005 0755 0644
set_perm "$syncthing_data_dir/syncthing" 0 3005 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755

# verify binary exists
if [ ! -f "$syncthing_data_dir/syncthing" ]; then
  abort "syncthing binary not found after installation"
fi

# create home directory (syncthing --home)
mkdir -p "$syncthing_data_dir/home"

# create autostart service script in service.d
ui_print "— creating autostart service script..."
cat > "$service_dir/syncthing_service.sh" <<EOF
#!/system/bin/sh

wait_for_data() {
    while [ ! -f "/data/system/packages.xml" ]; do
        sleep 5
    done
}

boot_timeout=0
until [ "\$(getprop init.svc.bootanim)" = "stopped" ] || [ \$boot_timeout -ge 30 ]; do
    sleep 5
    boot_timeout=\$((boot_timeout + 5))
done

wait_for_data

syncthing_bin="${runtime_dir}/syncthing"
syncthing_home="${runtime_dir}/home"
log_file="${runtime_dir}/service.log"

if [ -f "\$syncthing_bin" ]; then
    mkdir -p "\${syncthing_home}"
    user_storage="\$(ls -d /storage/emulated/* 2>/dev/null | head -1)"
    [ -z "\$user_storage" ] && user_storage="/storage/emulated/0"
    export HOME="\$user_storage"  # home is only for the path \`~\`
    \$syncthing_bin serve --home="\${syncthing_home}" --no-browser > "\$log_file" 2>&1 &
fi
EOF
set_perm "$service_dir/syncthing_service.sh" 0 0 0755

# also create service.sh inside module (works on APatch/KernelSU/Magisk)
cat > "$MODPATH/service.sh" <<EOF
#!/system/bin/sh

wait_for_data() {
    while [ ! -f "/data/system/packages.xml" ]; do
        sleep 5
    done
}

boot_timeout=0
until [ "\$(getprop init.svc.bootanim)" = "stopped" ] || [ \$boot_timeout -ge 30 ]; do
    sleep 5
    boot_timeout=\$((boot_timeout + 5))
done

wait_for_data

syncthing_bin="${runtime_dir}/syncthing"
syncthing_home="${runtime_dir}/home"
log_file="${runtime_dir}/service.log"

if [ -f "\$syncthing_bin" ]; then
    mkdir -p "\${syncthing_home}"
    user_storage="\$(ls -d /storage/emulated/* 2>/dev/null | head -1)"
    [ -z "\$user_storage" ] && user_storage="/storage/emulated/0"
    export HOME="\$user_storage"
    \$syncthing_bin serve --home="\${syncthing_home}" --no-browser > "\$log_file" 2>&1 &
fi
EOF
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/action.sh" 0 0 0755

# copy action.sh and service.sh to modules/<id>/ (APatch only copies module.prop)
if [ -n "$MODID" ]; then
  modules_dir="/data/adb/modules/$MODID"
  mkdir -p "$modules_dir"
  cp "$MODPATH/action.sh" "$modules_dir/action.sh" 2>/dev/null || true
  cp "$MODPATH/service.sh" "$modules_dir/service.sh" 2>/dev/null || true
  chmod 0755 "$modules_dir/action.sh" 2>/dev/null || true
  chmod 0755 "$modules_dir/service.sh" 2>/dev/null || true
fi

# update module name and description
sed -i "s/description=.*/description=syncthing $latest_version ($sync_arch) running with root privileges/" "$MODPATH/module.prop"
if [ "${KSU:-false}" = "true" ]; then
  sed -i "s/name=.*/name=syncthing4root for KernelSU/g" "$MODPATH/module.prop"
elif [ "${APATCH:-false}" = "true" ]; then
  sed -i "s/name=.*/name=syncthing4root for APatch/g" "$MODPATH/module.prop"
else
  sed -i "s/name=.*/name=syncthing4root for Magisk/g" "$MODPATH/module.prop"
fi

# extract webroot last
ui_print "— generating webui redirect..."
web_address="127.0.0.1:8384"
web_protocol="http"
if [ -f "$syncthing_data_dir/home/config.xml" ]; then
  parsed_addr=$(grep -A50 '<gui' "$syncthing_data_dir/home/config.xml" | grep '<address>' | sed 's/.*<address>//;s/<\/address>.*//' | head -1)
  [ -n "$parsed_addr" ] && web_address="$parsed_addr"
  parsed_tls=$(grep -A50 '<gui' "$syncthing_data_dir/home/config.xml" | grep -o 'tls="[^"]*"' | sed 's/tls="//;s/"//' | head -1)
  [ "$parsed_tls" = "true" ] && web_protocol="https"
fi
case "$web_address" in
  0.0.0.0:*) web_address="127.0.0.1${web_address#0.0.0.0}" ;;
esac
mkdir -p "$MODPATH/webroot"
cat > "$MODPATH/webroot/index.html" <<EOF
<!DOCTYPE html>
<script>
    document.location = '${web_protocol}://${web_address}/'
</script>
</html>
EOF

ui_print ""
ui_print "— syncthing $latest_version ($sync_arch) installed"
ui_print "— installation complete, please reboot your device"

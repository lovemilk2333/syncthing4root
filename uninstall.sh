#!/system/bin/sh

MODDIR=${0%/*}

rm -rf "$MODDIR/syncthing"

if [ -f "/data/adb/ksu/service.d/syncthing_service.sh" ]; then
  rm -f "/data/adb/ksu/service.d/syncthing_service.sh"
fi

if [ -f "/data/adb/service.d/syncthing_service.sh" ]; then
  rm -f "/data/adb/service.d/syncthing_service.sh"
fi

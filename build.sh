#!/bin/sh

set -e

cd "$(dirname "$0")"

mod_id=$(grep '^id=' module.prop | sed 's/id=//')
mod_ver=$(grep '^version=' module.prop | sed 's/version=//')

[ -z "$mod_id" ] && echo "failed to read id from module.prop" && exit 1
[ -z "$mod_ver" ] && echo "failed to read version from module.prop" && exit 1

# prepare staging directory
rm -rf build/
mkdir -p build || true

# compile web server
echo "— compiling web server (ARM64)..."
(cd web && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ../build/syncthing4root_webserver .)

# copy module assets into staging
echo "— copying module files..."
cp module.prop customize.sh action.sh uninstall.sh update.sh README.md LICENSE build/
cp -r META-INF build/

# create the zip from staging (everything in build/ goes in)
mkdir -p release
output="release/${mod_id}-${mod_ver}.zip"
rm -f "$output"
cd build
zip -r "../$output" .
cd ..

echo "created $output"

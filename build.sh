#!/bin/sh

set -e

cd "$(dirname "$0")"

mod_id=$(grep '^id=' module.prop | sed 's/id=//')
mod_ver=$(grep '^version=' module.prop | sed 's/version=//')

[ -z "$mod_id" ] && echo "failed to read id from module.prop" && exit 1
[ -z "$mod_ver" ] && echo "failed to read version from module.prop" && exit 1

mkdir -p release
output="release/${mod_id}-${mod_ver}.zip"

# respect .gitignore patterns (/workspace, /release)
zip -r "$output" . -x 'workspace/*' -x 'release/*' -x 'build.sh' -x '.gitignore' -x '.git/*'

echo "created $output"

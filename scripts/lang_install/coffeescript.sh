#!/bin/bash
set -e
# Portable CoffeeScript install. Debian: pinned npm package, then the global
# coffeescript package (pinned); Arch: coffeescript in pacman.
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing CoffeeScript..."
if [ "$(pkg_os)" = "debian" ]; then
    apt-get install -y --no-install-recommends npm=9.2.0~ds1-1
    npm install -g coffeescript@2.7.0
else
    pacman -S --needed --noconfirm coffeescript
fi

if command -v coffee &> /dev/null; then
    echo "CoffeeScript installation verified successfully."
    coffee --version
    printf 'console.log "coffee works: #{6*7}"\n' > /tmp/smoke.coffee
    (cd /tmp && coffee -c smoke.coffee && node smoke.js)
else
    echo "CoffeeScript installation verification failed"
    exit 1
fi
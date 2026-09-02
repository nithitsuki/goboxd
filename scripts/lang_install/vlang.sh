#!/bin/bash
set -e

# V from the official GitHub release zip. Distro-agnostic (glibc). The zip
# unpacks a single v/ directory holding the binary and vlib.
echo "Installing V..."
DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -f "$TC/v/v" ]; then
    [ -f "$DL/v.zip" ] || curl -fsSL -o "$DL/v.zip" \
        https://github.com/vlang/v/releases/download/0.4.9/v_linux.zip
    rm -rf "$TC/v"
    unzip -q "$DL/v.zip" -d "$TC"
fi

rm -rf /usr/local/v
cp -a "$TC/v" /usr/local/v

ln -sf /usr/local/v/v /usr/local/bin/v

if command -v v &> /dev/null; then
    echo "V installation verified successfully."
    v version
    mkdir -p /tmp/vtest && cd /tmp/vtest
    printf 'println("v works: ${6*7}")\n' > solution.v
    v -o solution solution.v && ./solution
else
    echo "V installation verification failed"
    exit 1
fi
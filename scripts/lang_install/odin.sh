#!/bin/bash
set -e

# Odin from the official GitHub dev-release tarball. Odin links with the
# clang toolchain at build time, so clang (LLVM 14 on bookworm) is a required
# apt/pacman dependency.
echo "Installing Odin..."
if command -v apt-get &>/dev/null; then
    [ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
    apt-get install -y --no-install-recommends clang-14=1:14.0.6-12 libclang-14-dev=1:14.0.6-12
    # Odin invokes `clang` by name; bookworm ships clang-14.
    ln -sf /usr/bin/clang-14 /usr/local/bin/clang
else
    pacman -S --needed --noconfirm clang
fi

DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -f "$TC/odin/odin" ]; then
    [ -f "$DL/odin.tar.gz" ] || curl -fsSL -o "$DL/odin.tar.gz" \
        https://github.com/odin-lang/Odin/releases/download/dev-2026-09/odin-linux-amd64-dev-2026-09.tar.gz
    rm -rf "$TC/odin"
    mkdir -p "$TC/odin"
    tar -xzf "$DL/odin.tar.gz" -C "$TC/odin" --strip-components=1
fi

rm -rf /usr/local/odin
cp -a "$TC/odin" /usr/local/odin

ln -sf /usr/local/odin/odin /usr/local/bin/odin

if command -v odin &> /dev/null; then
    echo "Odin installation verified successfully."
    odin version
    mkdir -p /tmp/odintest && cd /tmp/odintest
    cat > solution.odin <<'ODIN'
package main
import "core:fmt"
main :: proc() {
    fmt.println("odin works:", 6 * 7)
}
ODIN
    ODIN_ROOT=/usr/local/odin odin build solution.odin -file -out:solution && ./solution
else
    echo "Odin installation verification failed"
    exit 1
fi
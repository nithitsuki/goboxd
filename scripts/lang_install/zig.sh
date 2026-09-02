#!/bin/bash
set -e

# Zig from the official ziglang.org release tarball. Distro-agnostic.
echo "Installing Zig..."
DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/zig" ]; then
    [ -f "$DL/zig.tar.xz" ] || curl -fsSL -o "$DL/zig.tar.xz" \
        https://ziglang.org/download/0.14.1/zig-x86_64-linux-0.14.1.tar.xz
    rm -rf "$TC/zig"
    mkdir -p "$TC/zig"
    tar -xJf "$DL/zig.tar.xz" -C "$TC/zig" --strip-components=1
fi

rm -rf /usr/local/zig
cp -a "$TC/zig" /usr/local/zig

ln -sf /usr/local/zig/zig /usr/local/bin/zig

if command -v zig &> /dev/null; then
    echo "Zig installation verified successfully."
    zig version
    mkdir -p /tmp/zigtest && cd /tmp/zigtest
    cat > solution.zig <<'ZIG'
const std = @import("std");
pub fn main() !void {
    std.debug.print("zig works: {d}\n", .{6 * 7});
}
ZIG
    zig build-exe solution.zig -O ReleaseSafe -femit-bin=solution && ./solution
else
    echo "Zig installation verification failed"
    exit 1
fi
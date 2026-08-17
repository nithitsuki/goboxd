#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Swift (6.0.3)..."
# System dependencies for Swift's runtime.
pkg_install \
    binutils gcc git \
    curl icu edit ncurses python sqlite libxml2 \
    pkg-config tzdata

DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/swift/usr/bin" ]; then
    [ -f "$DL/swift.tar.gz" ] || curl -fsSL -o "$DL/swift.tar.gz" \
        https://download.swift.org/swift-6.0.3-release/debian12/swift-6.0.3-RELEASE/swift-6.0.3-RELEASE-debian12.tar.gz
    mkdir -p "$TC/swift"
    tar -xzf "$DL/swift.tar.gz" -C "$TC/swift" --strip-components=1
fi

rm -rf /usr/local/swift
cp -a "$TC/swift" /usr/local/swift

ln -sf /usr/local/swift/usr/bin/swiftc /usr/local/bin/swiftc
ln -sf /usr/local/swift/usr/bin/swift /usr/local/bin/swift

if command -v swiftc &>/dev/null; then
    echo "Swift installation verified successfully."
    swiftc --version
    printf 'print("Swift is working correctly!")\n' > /tmp/swifttest.swift
    swiftc -o /tmp/swifttest /tmp/swifttest.swift \
        -Xlinker -rpath -Xlinker /usr/local/swift/usr/lib
    /tmp/swifttest
else
    echo "Swift installation verification failed: swiftc not found"
    exit 1
fi

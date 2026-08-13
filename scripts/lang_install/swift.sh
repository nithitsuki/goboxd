#!/bin/bash
set -e

echo "Installing Swift (6.0.3 for Debian 12)..."
# binutils-gold is a virtual package provided by binutils (which ships the
# gold linker as /usr/bin/ld.gold), so the concrete package is pinned.
apt-get install -y --no-install-recommends \
    binutils=2.40-2 gcc=4:12.2.0-3 git=1:2.39.5-0+deb12u3 \
    libcurl4-openssl-dev=7.88.1-10+deb12u15 libedit-dev=3.1-20221030-2 libicu-dev=72.1-3+deb12u1 libncurses-dev=6.4-4 \
    libpython3-dev=3.11.2-1+b1 libsqlite3-dev=3.40.1-2+deb12u2 libxml2-dev=2.9.14+dfsg-1.3~deb12u6 \
    pkg-config=1.8.1-1 tzdata=2026b-0+deb12u1 uuid-dev=2.38.1-5+deb12u3

# Toolchain cache: /var/cache/goboxd-dl and /var/cache/goboxd-toolchains are
# BuildKit cache mounts (see Dockerfile). Downloads and extractions survive
# layer rebuilds, so only the final copy runs on every build.
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

if command -v swiftc &> /dev/null; then
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

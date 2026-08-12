#!/bin/bash
set -e

echo "Installing Swift (6.0.3 for Debian 12)..."
apt-get install -y --no-install-recommends \
    binutils-gold gcc git \
    libcurl4-openssl-dev libedit-dev libicu-dev libncurses-dev \
    libpython3-dev libsqlite3-dev libxml2-dev \
    pkg-config tzdata uuid-dev

curl -fsSL -o /tmp/swift.tar.gz \
    https://download.swift.org/swift-6.0.3-release/debian12/swift-6.0.3-RELEASE/swift-6.0.3-RELEASE-debian12.tar.gz

mkdir -p /usr/local/swift
tar -xzf /tmp/swift.tar.gz -C /usr/local/swift --strip-components=1

ln -sf /usr/local/swift/usr/bin/swiftc /usr/local/bin/swiftc
ln -sf /usr/local/swift/usr/bin/swift /usr/local/bin/swift

if command -v swiftc &> /dev/null; then
    echo "Swift installation verified successfully."
    swiftc --version
    # Smoke test: compile and run with the toolchain rpath.
    printf 'print("Swift is working correctly!")\n' > /tmp/swifttest.swift
    swiftc -o /tmp/swifttest /tmp/swifttest.swift \
        -Xlinker -rpath -Xlinker /usr/local/swift/usr/lib
    /tmp/swifttest
else
    echo "Swift installation verification failed: swiftc not found"
    exit 1
fi

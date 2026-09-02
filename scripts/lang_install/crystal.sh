#!/bin/bash
set -e

# Crystal from the official GitHub release. The "bundled" tarball carries
# its own LLVM runtime, so it runs on plain glibc Linux (debian bookworm
# and arch alike) without extra apt/pacman packages.
echo "Installing Crystal..."
DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/crystal/bin" ]; then
    [ -f "$DL/crystal.tar.gz" ] || curl -fsSL -o "$DL/crystal.tar.gz" \
        https://github.com/crystal-lang/crystal/releases/download/1.13.2/crystal-1.13.2-1-linux-x86_64-bundled.tar.gz
    rm -rf "$TC/crystal"
    mkdir -p "$TC/crystal"
    tar -xzf "$DL/crystal.tar.gz" -C "$TC/crystal" --strip-components=2
fi

rm -rf /usr/local/crystal
cp -a "$TC/crystal" /usr/local/crystal

ln -sf /usr/local/crystal/bin/crystal /usr/local/bin/crystal

if command -v crystal &> /dev/null; then
    echo "Crystal installation verified successfully."
    crystal --version
    mkdir -p /tmp/crystaltest && cd /tmp/crystaltest
    printf 'puts "crystal works: #{6*7}"\n' > solution.cr
    crystal build solution.cr -o solution && ./solution
else
    echo "Crystal installation verification failed"
    exit 1
fi
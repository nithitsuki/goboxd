#!/bin/bash
set -e

# Debian-only GNU Smalltalk install: gnu-smalltalk was dropped from Debian
# after bullseye, so bookworm has no binary package. Build 3.2.5 from the
# GNU ftp tarball. Arch does not package it either (AUR-only), so there is
# no pacman branch.
#
# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing GNU Smalltalk (source build)..."
apt-get install -y --no-install-recommends \
    build-essential=12.9 \
    gawk=1:5.2.1-2 \
    zip=3.0-13 \
    libsigsegv-dev=2.14-1 \
    libffi-dev=3.4.4-1 \
    libreadline-dev=8.2-1.3

DL=/var/cache/goboxd-dl
mkdir -p "$DL"

[ -f "$DL/smalltalk-3.2.5.tar.gz" ] || curl -fsSL -o "$DL/smalltalk-3.2.5.tar.gz" \
    https://ftp.gnu.org/gnu/smalltalk/smalltalk-3.2.5.tar.gz

rm -rf /tmp/smalltalk-src
mkdir -p /tmp/smalltalk-src
tar -xzf "$DL/smalltalk-3.2.5.tar.gz" -C /tmp/smalltalk-src --strip-components=1
(cd /tmp/smalltalk-src && ./configure --prefix=/usr/local --without-tcl --without-tk --without-x > /dev/null && make -j4 > /dev/null && make install > /dev/null)
rm -rf /tmp/smalltalk-src

if command -v gst &> /dev/null; then
    echo "GNU Smalltalk installation verified successfully."
    gst --version | head -1
    printf 'Transcript show: ''gst works: ''; Transcript show: (6*7) printString; Transcript nl.\n' > /tmp/smoke.st
    gst -q /tmp/smoke.st
else
    echo "GNU Smalltalk installation verification failed"
    exit 1
fi
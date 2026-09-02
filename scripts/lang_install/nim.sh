#!/bin/bash
set -e
# Portable Nim install (debian: apt pinned; arch: pacman).
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing Nim..."
pkg_install nim=1.6.10-2

if command -v nim &> /dev/null; then
    echo "Nim installation verified successfully."
    nim --version | head -1
    mkdir -p /tmp/nimtest && cd /tmp/nimtest
    printf 'echo "nim works: ", 6 * 7\n' > solution.nim
    nim c --hints:off -o:solution solution.nim && ./solution
else
    echo "Nim installation verification failed"
    exit 1
fi
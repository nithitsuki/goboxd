#!/bin/bash
set -e
# Portable dash install (debian: apt package; arch: pacman).
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing dash..."
pkg_install dash=0.5.12-2

if command -v dash &> /dev/null; then
    echo "dash installation verified successfully."
    dash -c 'echo "dash works: $((6 * 7))"'
else
    echo "dash installation verification failed"
    exit 1
fi
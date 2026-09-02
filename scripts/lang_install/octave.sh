#!/bin/bash
set -e
# Portable GNU Octave install (debian: apt pinned; arch: pacman).
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing GNU Octave..."
pkg_install octave=7.3.0-2

if command -v octave &> /dev/null; then
    echo "Octave installation verified successfully."
    octave --version | head -1
    printf 'printf("octave works: %%d\\n", 6*7);\n' > /tmp/smoke.m
    octave --no-gui --no-window-system --quiet /tmp/smoke.m
else
    echo "Octave installation verification failed"
    exit 1
fi
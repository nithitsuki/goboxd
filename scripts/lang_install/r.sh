#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing R..."
pkg_install R

if command -v R &>/dev/null; then
    echo "R installation verified successfully."
    R --version | head -1
else
    echo "R installation verification failed: R not found"
    exit 1
fi

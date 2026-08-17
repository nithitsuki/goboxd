#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing D / GDC..."
# gdc is the GCC-based D compiler. On arch it may not be available;
# install ldc (LLVM-based D compiler) as a fallback.
if ! pkg_install gdc 2>/dev/null; then
    echo "gdc not available, installing ldc as fallback..."
    pkg_install ldc
    if command -v ldc2 &>/dev/null; then
        ln -sf "$(which ldc2)" /usr/local/bin/gdc
    fi
fi

if command -v gdc &>/dev/null; then
    echo "GDC installation verified successfully."
    gdc --version | head -1
else
    echo "GDC installation verification failed: gdc not found"
    exit 1
fi

#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing LuaJIT..."
pkg_install luajit

if command -v luajit &>/dev/null; then
    echo "LuaJIT installation verified successfully."
    luajit -v
else
    echo "LuaJIT installation verification failed: luajit not found"
    exit 1
fi

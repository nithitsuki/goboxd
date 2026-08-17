#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Verilog (Icarus)..."
pkg_install iverilog

if command -v iverilog &>/dev/null; then
    echo "Verilog installation verified successfully."
    iverilog -V 2>&1 | head -1
else
    echo "Verilog installation verification failed: iverilog not found"
    exit 1
fi

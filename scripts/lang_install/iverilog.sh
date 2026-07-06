#!/bin/bash
set -e

echo "Installing Verilog (Icarus)..."
apt-get install -y --no-install-recommends iverilog

# Verify Icarus Verilog is working
if command -v iverilog &> /dev/null; then
    echo "Verilog installation verified successfully."
    iverilog -V 2>&1 | head -1
else
    echo "Verilog installation verification failed: iverilog not found"
    exit 1
fi

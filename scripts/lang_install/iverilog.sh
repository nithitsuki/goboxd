#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Verilog (Icarus)..."
apt-get install -y --no-install-recommends iverilog=11.0-1.1+b1

# Verify Icarus Verilog is working
if command -v iverilog &> /dev/null; then
    echo "Verilog installation verified successfully."
    iverilog -V 2>&1 | head -1
else
    echo "Verilog installation verification failed: iverilog not found"
    exit 1
fi

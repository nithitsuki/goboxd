#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing R..."
apt-get install -y --no-install-recommends r-base

# Verify R is working
if command -v R &> /dev/null; then
    echo "R installation verified successfully."
    R --version | head -1
else
    echo "R installation verification failed: R not found"
    exit 1
fi

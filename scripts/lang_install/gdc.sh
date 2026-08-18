#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing D / GDC..."
apt-get install -y --no-install-recommends gdc=4:12.2.0-3

# Verify GDC is working
if command -v gdc &> /dev/null; then
    echo "GDC installation verified successfully."
    gdc --version | head -1
else
    echo "GDC installation verification failed: gdc not found"
    exit 1
fi

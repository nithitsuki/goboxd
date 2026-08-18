#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Node.js..."
apt-get install -y --no-install-recommends nodejs=18.20.4+dfsg-1~deb12u2

# Verify Node.js is working
if command -v node &> /dev/null; then
    echo "Node.js installation verified successfully."
    node --version
    node -e "console.log('Node.js is working correctly!')"
else
    echo "Node.js installation verification failed"
    exit 1
fi

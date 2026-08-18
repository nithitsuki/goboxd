#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing LuaJIT..."
apt-get install -y --no-install-recommends luajit=2.1.0~beta3+git20220320+dfsg-4.1+deb12u1

# Verify LuaJIT is working
if command -v luajit &> /dev/null; then
    echo "LuaJIT installation verified successfully."
    luajit -v
else
    echo "LuaJIT installation verification failed: luajit not found"
    exit 1
fi

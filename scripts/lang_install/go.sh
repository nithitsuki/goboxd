#!/bin/bash
set -e
# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Go..."
apt-get install -y --no-install-recommends golang-go
echo "Go installation verified: $(go version)"

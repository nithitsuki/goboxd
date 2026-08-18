#!/bin/bash
set -e
# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Go..."
# golang-go is a metapackage that provides the /usr/bin/go symlink to
# golang-1.19-go, so it stays pinned as a metapackage.
apt-get install -y --no-install-recommends golang-go=2:1.19~1
echo "Go installation verified: $(go version)"

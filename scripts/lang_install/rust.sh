#!/bin/bash
set -e
# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Rust..."
apt-get install -y --no-install-recommends rustc cargo
echo "Rust installation verified: $(rustc --version)"

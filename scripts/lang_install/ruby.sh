#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Ruby..."
apt-get install -y --no-install-recommends ruby

if command -v ruby &> /dev/null; then
    echo "Ruby installation verified successfully."
    ruby --version
    ruby -e 'puts "Ruby is working correctly!"'
else
    echo "Ruby installation verification failed: ruby not found"
    exit 1
fi

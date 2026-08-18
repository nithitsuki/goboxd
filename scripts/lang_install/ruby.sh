#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Ruby..."
# ruby3.1 is the concrete package behind the ruby metapackage. It provides
# /usr/bin/ruby via the alternatives mechanism.
apt-get install -y --no-install-recommends ruby3.1=3.1.2-7+deb12u1

if command -v ruby &> /dev/null; then
    echo "Ruby installation verified successfully."
    ruby --version
    ruby -e 'puts "Ruby is working correctly!"'
else
    echo "Ruby installation verification failed: ruby not found"
    exit 1
fi

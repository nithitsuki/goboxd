#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing PHP..."
apt-get install -y --no-install-recommends php-cli php-bcmath

if command -v php &> /dev/null; then
    echo "PHP installation verified successfully."
    php --version
    php -r 'echo "PHP is working correctly!\n";'
    if php -m | grep -qi bcmath; then
        echo "bcmath module is present."
    else
        echo "WARNING: bcmath module not found."
        exit 1
    fi
else
    echo "PHP installation verification failed: php not found"
    exit 1
fi

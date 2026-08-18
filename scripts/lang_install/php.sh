#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing PHP..."
# php8.2-cli and php8.2-bcmath are the concrete packages behind the
# php-cli / php-bcmath metapackages. They provide /usr/bin/php and the
# bcmath extension.
apt-get install -y --no-install-recommends php8.2-cli=8.2.33-1~deb12u1 php8.2-bcmath=8.2.33-1~deb12u1

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

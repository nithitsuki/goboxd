#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing PHP..."
pkg_install php

if command -v php &>/dev/null; then
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

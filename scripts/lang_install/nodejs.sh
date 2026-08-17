#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Node.js..."
pkg_install nodejs

if command -v node &>/dev/null; then
    echo "Node.js installation verified successfully."
    node --version
    node -e "console.log('Node.js is working correctly!')"
else
    echo "Node.js installation verification failed"
    exit 1
fi

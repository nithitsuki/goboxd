#!/bin/bash
set -e

echo "Installing Node.js..."
apt-get install -y --no-install-recommends nodejs

# Verify Node.js is working
if command -v node &> /dev/null; then
    echo "Node.js installation verified successfully."
    node --version
    node -e "console.log('Node.js is working correctly!')"
else
    echo "Node.js installation verification failed"
    exit 1
fi

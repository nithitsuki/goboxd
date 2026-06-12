#!/bin/bash
set -e

echo "Installing D / GDC..."
apt-get install -y --no-install-recommends gdc

# Verify GDC is working
if command -v gdc &> /dev/null; then
    echo "GDC installation verified successfully."
    gdc --version | head -1
else
    echo "GDC installation verification failed: gdc not found"
    exit 1
fi

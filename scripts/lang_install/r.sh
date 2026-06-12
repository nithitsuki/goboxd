#!/bin/bash
set -e

echo "Installing R..."
apt-get install -y --no-install-recommends r-base

# Verify R is working
if command -v R &> /dev/null; then
    echo "R installation verified successfully."
    R --version | head -1
else
    echo "R installation verification failed: R not found"
    exit 1
fi

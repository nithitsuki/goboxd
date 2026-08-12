#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Haskell (GHC)..."
apt-get install -y --no-install-recommends ghc

# Verify Haskell is working
if command -v ghc &> /dev/null; then
    echo "Haskell installation verified successfully."
    ghc --version
else
    echo "Haskell installation verification failed: ghc not found"
    exit 1
fi

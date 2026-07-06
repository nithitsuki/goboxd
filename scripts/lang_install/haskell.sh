#!/bin/bash
set -e

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

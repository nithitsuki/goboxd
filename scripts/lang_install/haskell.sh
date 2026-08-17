#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Haskell (GHC)..."
pkg_install ghc

if command -v ghc &>/dev/null; then
    echo "Haskell installation verified successfully."
    ghc --version
else
    echo "Haskell installation verification failed: ghc not found"
    exit 1
fi

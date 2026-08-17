#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Ruby..."
pkg_install ruby

if command -v ruby &>/dev/null; then
    echo "Ruby installation verified successfully."
    ruby --version
    ruby -e 'puts "Ruby is working correctly!"'
else
    echo "Ruby installation verification failed: ruby not found"
    exit 1
fi

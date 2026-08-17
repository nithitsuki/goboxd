#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Rust..."
pkg_install rustc cargo
echo "Rust installation verified: $(rustc --version)"

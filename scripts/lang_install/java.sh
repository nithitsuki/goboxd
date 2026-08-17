#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Java..."
pkg_install java-environment

if command -v java &>/dev/null; then
    echo "Java installation verified successfully."
    java -version 2>&1 | head -1
else
    echo "Java installation verification failed"
    exit 1
fi

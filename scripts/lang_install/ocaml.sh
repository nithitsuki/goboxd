#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing OCaml..."
pkg_install ocaml

if command -v ocaml &>/dev/null; then
    echo "OCaml installation verified successfully."
    ocaml -version
else
    echo "OCaml installation verification failed: ocaml not found"
    exit 1
fi

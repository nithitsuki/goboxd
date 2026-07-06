#!/bin/bash
set -e

echo "Installing OCaml..."
apt-get install -y --no-install-recommends ocaml

# Verify OCaml is working
if command -v ocaml &> /dev/null; then
    echo "OCaml installation verified successfully."
    ocaml -version
else
    echo "OCaml installation verification failed: ocaml not found"
    exit 1
fi

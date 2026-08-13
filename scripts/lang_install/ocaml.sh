#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing OCaml..."
# ocaml is a metapackage; the ocamlopt binary that the registry uses comes
# from its ocaml-nox dependency chain, so the metapackage stays pinned.
apt-get install -y --no-install-recommends ocaml=4.13.1-4

# Verify OCaml is working
if command -v ocaml &> /dev/null; then
    echo "OCaml installation verified successfully."
    ocaml -version
else
    echo "OCaml installation verification failed: ocaml not found"
    exit 1
fi

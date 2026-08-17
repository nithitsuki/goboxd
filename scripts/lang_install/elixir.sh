#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Elixir..."
# Requires erlang first. On arch, install erlang alongside elixir.
pkg_install erlang elixir

if command -v elixir &>/dev/null; then
    echo "Elixir installation verified successfully."
    elixir --version
    elixir -e 'IO.puts("Elixir is working correctly!")'
else
    echo "Elixir installation verification failed: elixir not found"
    exit 1
fi

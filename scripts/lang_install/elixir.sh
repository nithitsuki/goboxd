#!/bin/bash
set -e

echo "Installing Elixir..."
# Requires the Erlang layer (erlang.sh) to run first in the Dockerfile.
apt-get install -y --no-install-recommends elixir

if command -v elixir &> /dev/null; then
    echo "Elixir installation verified successfully."
    elixir --version
    elixir -e 'IO.puts("Elixir is working correctly!")'
else
    echo "Elixir installation verification failed: elixir not found"
    exit 1
fi

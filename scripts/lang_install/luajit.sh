#!/bin/bash
set -e

echo "Installing LuaJIT..."
apt-get install -y --no-install-recommends luajit

# Verify LuaJIT is working
if command -v luajit &> /dev/null; then
    echo "LuaJIT installation verified successfully."
    luajit -v
else
    echo "LuaJIT installation verification failed: luajit not found"
    exit 1
fi

#!/bin/bash
set -e
echo "Installing Rust..."
apt-get install -y --no-install-recommends rustc cargo
echo "Rust installation verified: $(rustc --version)"

#!/bin/bash
set -e

echo "Installing Ruby..."
apt-get install -y --no-install-recommends ruby

if command -v ruby &> /dev/null; then
    echo "Ruby installation verified successfully."
    ruby --version
    ruby -e 'puts "Ruby is working correctly!"'
else
    echo "Ruby installation verification failed: ruby not found"
    exit 1
fi

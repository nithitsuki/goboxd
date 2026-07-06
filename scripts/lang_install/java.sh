#!/bin/bash
set -e

echo "Installing Java..."
apt-get install -y --no-install-recommends default-jdk

# Verify Java is working
if command -v java &> /dev/null; then
    echo "Java installation verified successfully."
    java -version 2>&1 | head -1
else
    echo "Java installation verification failed"
    exit 1
fi

#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Java..."
# openjdk-17-jdk-headless is the concrete package behind the default-jdk
# metapackage. It provides /usr/bin/java and /usr/bin/javac.
apt-get install -y --no-install-recommends openjdk-17-jdk-headless=17.0.20.1+1-1~deb12u1

# Verify Java is working
if command -v java &> /dev/null; then
    echo "Java installation verified successfully."
    java -version 2>&1 | head -1
else
    echo "Java installation verification failed"
    exit 1
fi

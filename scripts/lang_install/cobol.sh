#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing GnuCOBOL..."
# gnucobol is a metapackage (version 5) that pulls gnucobol3, the real
# compiler. Pin the compiler itself so a metapackage bump cannot float the
# toolchain.
apt-get install -y --no-install-recommends gnucobol3=3.1.2-5+b1

if command -v cobc &> /dev/null; then
    echo "GnuCOBOL installation verified successfully."
    cobc --version | head -1
else
    echo "GnuCOBOL installation verification failed"
    exit 1
fi
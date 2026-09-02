#!/bin/bash
set -e

# Debian-only Raku install: Debian bookworm ships rakudo (the MoarVM-based
# Raku) but Arch does not (AUR-only there), so no pacman branch.
#
# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Raku (rakudo)..."
apt-get install -y --no-install-recommends rakudo=2022.12-1

if command -v raku &> /dev/null; then
    echo "Raku installation verified successfully."
    raku --version | head -1
    printf 'say "raku works: ", 6*7;\n' > /tmp/smoke.p6
    raku /tmp/smoke.p6
else
    echo "Raku installation verification failed"
    exit 1
fi
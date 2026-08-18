#!/usr/bin/env bash
# Install and verify gawk (GNU AWK).
set -euo pipefail

GAWK_VERSION="1:5.2.1-2"

apt-get install -y "gawk=${GAWK_VERSION}"

# Verify: run a real script through the interpreter.
gawk 'BEGIN { print "gawk OK" }' | grep -q "gawk OK"

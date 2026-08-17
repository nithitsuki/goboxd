#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

pkg_install gawk
gawk 'BEGIN { print "gawk OK" }' | grep -q "gawk OK"

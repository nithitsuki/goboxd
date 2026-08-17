#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Free Pascal..."
pkg_install fpc

if command -v fpc &>/dev/null; then
    echo "Free Pascal installation verified successfully."
    fpc -iV
    mkdir -p /tmp/pascaltest
    cat > /tmp/pascaltest/solution.pas <<'PAS'
program P;
begin
  writeln('Pascal is working correctly!');
end.
PAS
    (cd /tmp/pascaltest && fpc -osolution solution.pas > /dev/null && ./solution)
else
    echo "Free Pascal installation verification failed: fpc not found"
    exit 1
fi

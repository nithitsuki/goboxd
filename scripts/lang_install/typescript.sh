#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing TypeScript compiler..."
pkg_install nodejs npm

npm install -g --cache /var/cache/goboxd-npm --prefer-offline typescript@5.7.3 @types/node@18.19.130

if command -v tsc &>/dev/null; then
    echo "TypeScript installation verified successfully."
    tsc --version
    mkdir -p /tmp/tstest
    printf 'const fs = require("fs");\nconsole.log("TypeScript is working correctly!");\n' > /tmp/tstest/solution.ts
    (cd /tmp/tstest && tsc solution.ts --target ES2024 --module commonjs --strict --skipLibCheck --typeRoots /usr/local/lib/node_modules/@types)
    node /tmp/tstest/solution.js
else
    echo "TypeScript installation verification failed: tsc not found"
    exit 1
fi

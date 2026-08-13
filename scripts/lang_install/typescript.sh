#!/bin/bash
set -e

echo "Installing TypeScript compiler..."
apt-get install -y --no-install-recommends nodejs=18.20.4+dfsg-1~deb12u2 npm=9.2.0~ds1-1

# npm cache mount: /var/cache/goboxd-npm persists package downloads across
# layer rebuilds. typescript is pinned to an exact version; @types/node is
# pinned to an exact 18.x version instead of the floating @18 range.
npm install -g --cache /var/cache/goboxd-npm --prefer-offline typescript@5.7.3 @types/node@18.19.130

if command -v tsc &> /dev/null; then
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

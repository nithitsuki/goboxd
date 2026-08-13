#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Free Pascal..."
apt-get install -y --no-install-recommends fp-compiler=3.2.2+dfsg-20 fp-units-net=3.2.2+dfsg-20

if command -v fpc &> /dev/null; then
    echo "Free Pascal installation verified successfully."
    fpc -iV
    # Smoke test: compile and run.
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

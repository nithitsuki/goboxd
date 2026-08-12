#!/bin/bash
set -e

echo "Installing Dart SDK..."
curl -fsSL -o /tmp/dart.zip \
    https://storage.googleapis.com/dart-archive/channels/stable/release/3.2.6/sdk/dartsdk-linux-x64-release.zip

unzip -q /tmp/dart.zip -d /usr/local/
mv /usr/local/dart-sdk /usr/local/dart

ln -sf /usr/local/dart/bin/dart /usr/local/bin/dart

# Wrapper for the jail: dart compile writes its AOT snapshot to a temp dir and
# renames it to the output. The jail mounts /app rw and /tmp as tmpfs, so the
# cross-filesystem rename fails. Force TMPDIR onto the writable /app.
cat > /usr/local/bin/dart-compile <<'WRAPPER'
#!/bin/bash
export TMPDIR=/app
exec /usr/local/dart/bin/dart "$@"
WRAPPER
chmod +x /usr/local/bin/dart-compile

if command -v dart &> /dev/null; then
    echo "Dart installation verified successfully."
    dart --version
    # Smoke test: AOT compile + run.
    mkdir -p /tmp/darttest
    printf "void main() { print('Dart is working correctly!'); }\n" > /tmp/darttest/solution.dart
    (cd /tmp/darttest && /usr/local/dart/bin/dart compile exe solution.dart -o solution > /dev/null && ./solution)
else
    echo "Dart installation verification failed: dart not found"
    exit 1
fi

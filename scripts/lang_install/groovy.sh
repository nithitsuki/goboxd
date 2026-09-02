#!/bin/bash
set -e

# Apache Groovy from the official Apache archive (not in Debian repos).
# Distro-agnostic zip install.
echo "Installing Groovy..."
if ! command -v java &> /dev/null; then
    echo "Groovy needs Java. Install the java language layer first (LANGS=java,groovy)." >&2
    exit 1
fi

DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/groovy/bin" ]; then
    [ -f "$DL/groovy.zip" ] || curl -fsSL -o "$DL/groovy.zip" \
        https://archive.apache.org/dist/groovy/4.0.24/distribution/apache-groovy-binary-4.0.24.zip
    rm -rf "$TC/groovy"
    unzip -q "$DL/groovy.zip" -d "$TC"
    mv "$TC/groovy-4.0.24" "$TC/groovy"
fi

rm -rf /usr/local/groovy
cp -a "$TC/groovy" /usr/local/groovy

ln -sf /usr/local/groovy/bin/groovy /usr/local/bin/groovy

if command -v groovy &> /dev/null; then
    echo "Groovy installation verified successfully."
    groovy --version
    printf 'println "groovy works: ${6*7}"\n' > /tmp/smoke.groovy
    groovy /tmp/smoke.groovy
else
    echo "Groovy installation verification failed"
    exit 1
fi
#!/bin/bash
set -e

echo "Installing Kotlin..."
curl -fsSL -o /tmp/kotlin.zip \
    https://github.com/JetBrains/kotlin/releases/download/v2.1.10/kotlin-compiler-2.1.10.zip

unzip -q /tmp/kotlin.zip -d /usr/local/
# Extracts to /usr/local/kotlinc

ln -sf /usr/local/kotlinc/bin/kotlinc /usr/local/bin/kotlinc
ln -sf /usr/local/kotlinc/bin/kotlin /usr/local/bin/kotlin

if command -v kotlinc &> /dev/null; then
    echo "Kotlin installation verified successfully."
    kotlinc -version
    # Smoke test using the real script path (it derives libs from $0).
    mkdir -p /tmp/kotlintest
    printf 'fun main() { println("Kotlin is working correctly!") }\n' > /tmp/kotlintest/solution.kt
    (cd /tmp/kotlintest && /usr/local/kotlinc/bin/kotlinc solution.kt -include-runtime -d solution.jar && /usr/bin/java -jar solution.jar)
else
    echo "Kotlin installation verification failed: kotlinc not found"
    exit 1
fi

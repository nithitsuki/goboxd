#!/bin/bash
set -e

echo "Installing Kotlin..."
DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/kotlinc/bin" ]; then
    [ -f "$DL/kotlin.zip" ] || curl -fsSL -o "$DL/kotlin.zip" \
        https://github.com/JetBrains/kotlin/releases/download/v2.1.10/kotlin-compiler-2.1.10.zip
    unzip -q "$DL/kotlin.zip" -d "$TC"
fi

rm -rf /usr/local/kotlinc
cp -a "$TC/kotlinc" /usr/local/kotlinc

ln -sf /usr/local/kotlinc/bin/kotlinc /usr/local/bin/kotlinc
ln -sf /usr/local/kotlinc/bin/kotlin /usr/local/bin/kotlin

echo "Kotlin installation verified: $(/usr/local/kotlinc/bin/kotlinc -version 2>&1)"
printf 'fun main() { println("kotlin smoke test") }\n' > /tmp/smoke.kt
/usr/local/kotlinc/bin/kotlinc /tmp/smoke.kt -include-runtime -d /tmp/smoke.jar
/usr/bin/java -jar /tmp/smoke.jar

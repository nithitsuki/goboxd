#!/bin/bash
set -e

echo "Installing Scala 3..."
curl -fsSL -o /tmp/scala3.tar.gz \
    https://github.com/scala/scala3/releases/download/3.3.1/scala3-3.3.1.tar.gz

mkdir -p /usr/local/scala3
tar -xzf /tmp/scala3.tar.gz -C /usr/local/scala3 --strip-components=1

ln -sf /usr/local/scala3/bin/scalac /usr/local/bin/scalac
ln -sf /usr/local/scala3/bin/scala /usr/local/bin/scala

if command -v scalac &> /dev/null; then
    echo "Scala installation verified successfully."
    scalac -version
    # Smoke test using the real script paths (they derive libs from $0).
    mkdir -p /tmp/scalatest
    printf 'object Solution { def main(args: Array[String]): Unit = { println("Scala is working correctly!") } }\n' > /tmp/scalatest/solution.scala
    (cd /tmp/scalatest && /usr/local/scala3/bin/scalac -d . solution.scala && /usr/local/scala3/bin/scala Solution)
else
    echo "Scala installation verification failed: scalac not found"
    exit 1
fi

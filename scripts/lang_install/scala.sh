#!/bin/bash
set -e

echo "Installing Scala..."
DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/scala3/bin" ]; then
    [ -f "$DL/scala3.tar.gz" ] || curl -fsSL -o "$DL/scala3.tar.gz" \
        https://github.com/scala/scala3/releases/download/3.3.1/scala3-3.3.1.tar.gz
    mkdir -p "$TC/scala3"
    tar -xzf "$DL/scala3.tar.gz" -C "$TC/scala3" --strip-components=1
fi

rm -rf /usr/local/scala3
cp -a "$TC/scala3" /usr/local/scala3

ln -sf /usr/local/scala3/bin/scalac /usr/local/bin/scalac
ln -sf /usr/local/scala3/bin/scala /usr/local/bin/scala

echo "Scala installation verified: $(/usr/local/scala3/bin/scalac -version 2>&1)"
printf 'object Main { def main(args: Array[String]): Unit = { println("scala smoke test") } }\n' > /tmp/smoke.scala
cd /tmp && /usr/local/scala3/bin/scalac -d . smoke.scala && /usr/local/scala3/bin/scala Main

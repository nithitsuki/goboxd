#!/bin/bash
set -e
# Portable .NET (Mono) install. The backlog "dotnet" entry is the Mono
# toolchain (mcs compiler + mono runtime), separate from the .NET 10 SDK
# used by csharp. Debian: mono-devel (pinned). Arch: mono (same name).
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing Mono (.NET) toolchain..."
pkg_install mono-devel=6.8.0.105+dfsg-3.3+deb12u1

if command -v mcs &> /dev/null && command -v mono &> /dev/null; then
    echo ".NET (Mono) installation verified successfully."
    mcs --version | head -1
    mkdir -p /tmp/monotest
    printf 'using System;\nclass MainClass { public static void Main() { Console.WriteLine("mono works: " + (6*7)); } }\n' > /tmp/monotest/solution.cs
    (cd /tmp/monotest && mcs -out:solution.exe solution.cs && mono solution.exe)
else
    echo ".NET (Mono) installation verification failed"
    exit 1
fi
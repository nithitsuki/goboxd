#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing .NET SDK..."
# Pre-requisites for the .NET SDK installer.
pkg_install curl icu openssl zlib

DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/dotnet/sdk" ]; then
    [ -f "$DL/dotnet-install.sh" ] || curl -fsSL https://dot.net/v1/dotnet-install.sh -o "$DL/dotnet-install.sh"
    bash "$DL/dotnet-install.sh" --version 10.0.400 --install-dir "$TC/dotnet" --no-path
fi

rm -rf /usr/local/dotnet
cp -a "$TC/dotnet" /usr/local/dotnet

ln -sf /usr/local/dotnet/dotnet /usr/local/bin/dotnet

cat > /usr/local/bin/csharp-build <<'WRAPPER'
#!/bin/bash
set -e
SRC="$1"
export DOTNET_CLI_TELEMETRY_OPTOUT=1
export DOTNET_NOLOGO=1
export DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1
CSC=$(ls /usr/local/dotnet/sdk/*/Roslyn/bincore/csc.dll | tail -1)
REFPACK=$(ls -d /usr/local/dotnet/packs/Microsoft.NETCore.App.Ref/*/ref/net10.0 | tail -1)
{
    printf -- "-nologo\n-target:exe\n-out:solution.dll\n"
    for dll in "$REFPACK"/*.dll; do
        printf -- "-r:%s\n" "$dll"
    done
    printf -- "%s\n" "$SRC"
} > csc.rsp
/usr/local/dotnet/dotnet "$CSC" @csc.rsp
printf '{"runtimeOptions":{"tfm":"net10.0","framework":{"name":"Microsoft.NETCore.App","version":"10.0.0"}}}' > solution.runtimeconfig.json
WRAPPER
chmod +x /usr/local/bin/csharp-build

if command -v dotnet &> /dev/null; then
    echo ".NET installation verified successfully."
    dotnet --version
    mkdir -p /tmp/cstest
    printf 'using System; class P { static void Main() { Console.WriteLine("C# is working correctly!"); } }\n' > /tmp/cstest/solution.cs
    (cd /tmp/cstest && /usr/local/bin/csharp-build solution.cs && dotnet solution.dll)
else
    echo ".NET installation verification failed: dotnet not found"
    exit 1
fi

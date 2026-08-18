#!/bin/bash
set -e

echo "Installing .NET SDK..."
# Pin the SDK to an exact version for reproducible builds. The --channel
# form floats to the newest SDK. To bump the SDK, read the release index at
# https://dotnetcli.blob.core.windows.net/dotnet/release-metadata/releases-index.json
# and update the version below. Old SDK versions can disappear from the CDN,
# so keep the pin current.
apt-get install -y --no-install-recommends curl=7.88.1-10+deb12u15 libicu72=72.1-3+deb12u1 libssl3=3.0.20-1~deb12u2 zlib1g=1:1.2.13.dfsg-1

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

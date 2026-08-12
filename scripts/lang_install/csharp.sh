#!/bin/bash
set -e

echo "Installing .NET SDK (for C#)..."
apt-get install -y --no-install-recommends curl libicu72 libssl3 zlib1g

curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh
bash /tmp/dotnet-install.sh --channel 10.0 --install-dir /usr/local/dotnet --no-path

ln -sf /usr/local/dotnet/dotnet /usr/local/bin/dotnet

# Wrapper that compiles a single .cs file offline with the SDK's Roslyn
# compiler, referencing the bundled reference assemblies, then writes the
# runtimeconfig.json required by `dotnet solution.dll`.
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
    # Smoke test the wrapper.
    mkdir -p /tmp/cstest
    printf 'using System; class P { static void Main() { Console.WriteLine("C# is working correctly!"); } }\n' > /tmp/cstest/solution.cs
    (cd /tmp/cstest && /usr/local/bin/csharp-build solution.cs && dotnet solution.dll)
else
    echo ".NET installation verification failed: dotnet not found"
    exit 1
fi

#!/bin/bash
set -e

# PowerShell Core from the official GitHub release tarball. Distro-agnostic
# glibc build. Inside the jail the .NET globalization invariant is set by
# the runner (jailEnv), so no ICU is needed at run time; the host-side
# probe still gets ICU from the distro so pwsh --version can load.
echo "Installing PowerShell..."
if command -v apt-get &>/dev/null; then
    [ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
    apt-get install -y --no-install-recommends libicu72=72.1-3+deb12u1
else
    pacman -S --needed --noconfirm icu
fi

DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/powershell" ]; then
    [ -f "$DL/powershell.tar.gz" ] || curl -fsSL -o "$DL/powershell.tar.gz" \
        https://github.com/PowerShell/PowerShell/releases/download/v7.6.5/powershell-7.6.5-linux-x64.tar.gz
    rm -rf "$TC/powershell"
    mkdir -p "$TC/powershell"
    tar -xzf "$DL/powershell.tar.gz" -C "$TC/powershell"
fi

rm -rf /usr/local/powershell
cp -a "$TC/powershell" /usr/local/powershell

# The release tarball ships pwsh without the executable bit.
chmod +x /usr/local/powershell/pwsh
ln -sf /usr/local/powershell/pwsh /usr/local/bin/pwsh

if command -v pwsh &> /dev/null; then
    echo "PowerShell installation verified successfully."
    pwsh --version
    printf 'Write-Output "pwsh works: $($(6*7))"\n' > /tmp/smoke.ps1
    pwsh -NoProfile -NonInteractive -File /tmp/smoke.ps1
else
    echo "PowerShell installation verification failed"
    exit 1
fi
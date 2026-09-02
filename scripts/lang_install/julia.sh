#!/bin/bash
set -e

# Julia from the official julialang-s3 release tarball. Distro-agnostic.
echo "Installing Julia..."
DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/julia/bin" ]; then
    [ -f "$DL/julia.tar.gz" ] || curl -fsSL -o "$DL/julia.tar.gz" \
        https://julialang-s3.julialang.org/bin/linux/x64/1.11/julia-1.11.2-linux-x86_64.tar.gz
    rm -rf "$TC/julia"
    mkdir -p "$TC/julia"
    tar -xzf "$DL/julia.tar.gz" -C "$TC/julia" --strip-components=1
fi

rm -rf /usr/local/julia
cp -a "$TC/julia" /usr/local/julia

ln -sf /usr/local/julia/bin/julia /usr/local/bin/julia

if command -v julia &> /dev/null; then
    echo "Julia installation verified successfully."
    julia --version
    printf 'println("julia works: ", 6*7)\n' > /tmp/smoke.jl
    julia /tmp/smoke.jl
else
    echo "Julia installation verification failed"
    exit 1
fi
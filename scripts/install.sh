#!/bin/bash
set -e

echo "Installing system dependencies..."
apt-get update || (sleep 10 && apt-get update) || (sleep 30 && apt-get update)

apt-get install -y --no-install-recommends \
    ca-certificates curl wget unzip build-essential \
    gcc g++ \
    libprotobuf32 libprotobuf-c1 libnl-3-200 libnl-route-3-200

echo "Installing language toolchains..."
# Python 3 (interpreted)
apt-get install -y --no-install-recommends python3 python3-pip python3-dev

# Java (in-scope)
apt-get install -y --no-install-recommends default-jdk

# JavaScript / Node.js (in-scope)
apt-get install -y --no-install-recommends nodejs

# Haskell (functional)
apt-get install -y --no-install-recommends ghc

# OCaml (functional)
apt-get install -y --no-install-recommends ocaml

# R (statistical)
apt-get install -y --no-install-recommends r-base

# D / GDC (systems)
apt-get install -y --no-install-recommends gdc

# LuaJIT (scripting)
apt-get install -y --no-install-recommends luajit

# Verilog (in-scope)
apt-get install -y --no-install-recommends iverilog

# Rust (bonus language)
apt-get install -y --no-install-recommends rustc cargo

# Go (bonus language)
apt-get install -y --no-install-recommends golang-go

# Verify
python3 --version && gcc --version && g++ --version
java -version 2>&1 | head -1
node --version
which iverilog 2>/dev/null && iverilog -V 2>&1 | head -1 || echo "iverilog not installed"
which rustc 2>/dev/null && rustc --version || echo "rustc not installed"
which go 2>/dev/null && go version || echo "go not installed"

apt-get clean
rm -rf /var/lib/apt/lists/*

#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LANG_DIR="$SCRIPT_DIR/lang_install"

echo "==========================================="
echo "  goboxd - Full Language Installation"
echo "==========================================="

# Step 1: System dependencies
echo ""
echo "==> Step 1: Installing system dependencies..."
"$LANG_DIR/system.sh"

# Step 2: Language toolchains
echo ""
echo "==> Step 2: Installing language toolchains..."

# C
echo "  -> C"
"$LANG_DIR/c.sh"

# C++
echo "  -> C++"
"$LANG_DIR/cpp.sh"

# Python 3
echo "  -> Python 3"
"$LANG_DIR/python3.sh"

# Java
echo "  -> Java"
"$LANG_DIR/java.sh"

# Node.js / JavaScript
echo "  -> Node.js"
"$LANG_DIR/nodejs.sh"

# Haskell
echo "  -> Haskell"
"$LANG_DIR/haskell.sh"

# OCaml
echo "  -> OCaml"
"$LANG_DIR/ocaml.sh"

# R
echo "  -> R"
"$LANG_DIR/r.sh"

# D / GDC
echo "  -> D / GDC"
"$LANG_DIR/gdc.sh"

# LuaJIT
echo "  -> LuaJIT"
"$LANG_DIR/luajit.sh"

# Verilog
echo "  -> Verilog"
"$LANG_DIR/iverilog.sh"

# Rust
echo "  -> Rust"
"$LANG_DIR/rust.sh"

# Go
echo "  -> Go"
"$LANG_DIR/go.sh"

# Erlang
echo "  -> Erlang"
"$LANG_DIR/erlang.sh"

# Lisp
echo "  -> Lisp"
"$LANG_DIR/lisp.sh"

# Step 3: Final verification
echo ""
echo "==> Step 3: Final verification..."
echo ""

python3 --version && gcc --version && g++ --version
java -version 2>&1 | head -1
node --version
which iverilog 2>/dev/null && iverilog -V 2>&1 | head -1 || echo "iverilog not installed"
which rustc 2>/dev/null && rustc --version || echo "rustc not installed"
which go 2>/dev/null && go version || echo "go not installed"
which erl 2>/dev/null && erl -noshell -eval 'io:format("Erlang ~s~n", [erlang:system_info(otp_release)]), halt().' || echo "erlang not installed"
which sbcl 2>/dev/null && sbcl --version || echo "sbcl not installed"

echo ""
echo "==========================================="
echo "  Installation complete!"
echo "==========================================="

# Clean up
apt clean
rm -rf /var/lib/apt/lists/*

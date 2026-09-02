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

# --- Language backlog (2026-09-02) ---

# Clojure
echo "  -> Clojure"
"$LANG_DIR/clojure.sh"

# COBOL
echo "  -> COBOL"
"$LANG_DIR/cobol.sh"

# CoffeeScript
echo "  -> CoffeeScript"
"$LANG_DIR/coffeescript.sh"

# Crystal
echo "  -> Crystal"
"$LANG_DIR/crystal.sh"

# Dash
echo "  -> Dash"
"$LANG_DIR/dash.sh"

# .NET (Mono)
echo "  -> .NET (Mono)"
"$LANG_DIR/dotnet.sh"

# Dragon — deferred: v1.0.7's show/showln print nothing to stdout or stderr
# (verified on the official binary; even under a PTY). A language that cannot
# produce output cannot pass fixtures. Re-add when the toolchain's stdout
# works (script and pin were verified up to that blocker).

# Emacs Lisp
echo "  -> Emacs Lisp"
"$LANG_DIR/elisp.sh"

# FreeBASIC — deferred: the pinned fbc 1.10.1 runtime calls ioperm at program
# startup, which the global seccomp deny-list kills with SIGSYS (verified in a
# bookworm container 2026-09-02: exit 159 on both build and run). The
# per-language seccomp mechanism is additive-deny only, so ioperm cannot be
# re-allowed without weakening the global policy. Re-add if a future fbc
# drops the ioperm call or a per-language allow mechanism lands.

# Groovy
echo "  -> Groovy"
"$LANG_DIR/groovy.sh"

# Julia
echo "  -> Julia"
"$LANG_DIR/julia.sh"

# NASM
echo "  -> NASM"
"$LANG_DIR/nasm.sh"

# Nim
echo "  -> Nim"
"$LANG_DIR/nim.sh"

# Octave
echo "  -> Octave"
"$LANG_DIR/octave.sh"

# Odin
echo "  -> Odin"
"$LANG_DIR/odin.sh"

# Pony
echo "  -> Pony"
"$LANG_DIR/pony.sh"

# Prolog
echo "  -> Prolog"
"$LANG_DIR/prolog.sh"

# Pure — deferred: pure 0.68 only supports LLVM 2.5-3.5 (pre-MCJIT JIT;
# upstream port-to-MCJIT issue open since 2015); bookworm ships LLVM 13+ and
# the build fails at ExecutionEngine/JIT.h. Re-add if pure ever lands a
# modern-LLVM port.

# PowerShell
echo "  -> PowerShell"
"$LANG_DIR/pwsh.sh"

# Raku
echo "  -> Raku"
"$LANG_DIR/raku.sh"

# GNU Smalltalk
echo "  -> GNU Smalltalk"
"$LANG_DIR/smalltalk.sh"

# V
echo "  -> V"
"$LANG_DIR/vlang.sh"

# Zig
echo "  -> Zig"
"$LANG_DIR/zig.sh"

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

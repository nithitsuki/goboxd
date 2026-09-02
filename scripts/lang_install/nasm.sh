#!/bin/bash
set -e
# Portable NASM install (debian: apt pinned; arch: pacman). The assembler
# needs a linker for the final binary, so a nasm-build wrapper drives
# nasm + gcc (present via build-essential) inside the jail.
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing NASM..."
pkg_install nasm=2.16.01-1

# Wrapper for the jail build stage: assemble to ELF64 and link with gcc
# (-nostdlib: crt1.o defines its own _start, so linking it would collide
# with the asm _start; -no-pie keeps the flat address space under the
# exact RLIMIT_AS guard).
cat > /usr/local/bin/nasm-build <<'WRAPPER'
#!/bin/bash
set -e
SRC="$1"
BASE="${SRC%.asm}"
nasm -f elf64 "$SRC" -o "$BASE.o"
gcc -nostdlib -no-pie -o solution "$BASE.o"
WRAPPER
chmod +x /usr/local/bin/nasm-build

if command -v nasm &> /dev/null; then
    echo "NASM installation verified successfully."
    nasm -v
    mkdir -p /tmp/nasmtest && cd /tmp/nasmtest
    cat > solution.asm <<'ASM'
global _start
section .text
_start:
    mov rax, 1
    mov rdi, 1
    mov rsi, msg
    mov rdx, len
    syscall
    mov rax, 60
    xor rdi, rdi
    syscall
section .data
msg db 'nasm works', 10
len equ $ - msg
ASM
    nasm -f elf64 solution.asm -o solution.o && gcc -nostdlib -no-pie -o solution solution.o && ./solution
else
    echo "NASM installation verification failed"
    exit 1
fi
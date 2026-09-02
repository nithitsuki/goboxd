#!/bin/bash
set -e

# Pony from the official GitHub release tarball (the ubuntu22.04 build runs
# on bookworm's newer glibc). ponyc requires its source file to live in a
# directory named after the package, so a pony-build wrapper repackages the
# request file into main/ before compiling.
#
# The linked LLVM 15.0.7 runtime is bundled in the tarball, but ponyc still
# shells out to a system clang as the final linker (it falls back to the
# hard-coded /usr/bin/clang when $CC is unset), so pin clang-14 like odin.
if command -v apt-get &>/dev/null; then
    [ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
    apt-get install -y --no-install-recommends clang-14=1:14.0.6-12
    [ -e /usr/bin/clang ] || ln -s /usr/bin/clang-14 /usr/bin/clang
else
    pacman -S --needed --noconfirm clang
fi

echo "Installing Pony..."
DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/pony/bin" ]; then
    [ -f "$DL/pony.tar.gz" ] || curl -fsSL -o "$DL/pony.tar.gz" \
        https://github.com/ponylang/ponyc/releases/download/0.58.6/ponyc-x86-64-unknown-linux-ubuntu22.04.tar.gz
    rm -rf "$TC/pony"
    mkdir -p "$TC/pony"
    tar -xzf "$DL/pony.tar.gz" -C "$TC/pony" --strip-components=1
fi

rm -rf /usr/local/pony
cp -a "$TC/pony" /usr/local/pony

ln -sf /usr/local/pony/bin/ponyc /usr/local/bin/ponyc

# Wrapper for the jail build stage: ponyc compiles the package named by the
# containing directory, so the request file becomes main/main.pony. Run from
# inside main/ (ponyc takes no package arg there) and rename the resulting
# `main` binary to ./solution (the registry artifact name).
cat > /usr/local/bin/pony-build <<'WRAPPER'
#!/bin/bash
set -e
SRC="$1"
rm -rf main
mkdir -p main
cp "$SRC" main/main.pony
(cd main && ponyc -o . > /dev/null)
mv main/main ./solution
WRAPPER
chmod +x /usr/local/bin/pony-build

if command -v ponyc &> /dev/null; then
    echo "Pony installation verified successfully."
    ponyc --version
    mkdir -p /tmp/ponytest && cd /tmp/ponytest
    mkdir -p main
    cat > main/main.pony <<'PONY'
actor Main
  new create(env: Env) =>
    let n: U32 = 6 * 7
    env.out.print("pony works: " + n.string())
PONY
    (cd main && ponyc -o . > /dev/null) && mv main/main ./solution && ./solution
else
    echo "Pony installation verification failed"
    exit 1
fi
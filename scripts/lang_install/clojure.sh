#!/bin/bash
set -e

# Clojure CLI (tools.deps) from the official download host. Distro-agnostic
# tarball install; every glibc Linux works.
echo "Installing Clojure..."
if ! command -v java &> /dev/null; then
    echo "Clojure needs Java. Install the java language layer first (LANGS=java,clojure)." >&2
    exit 1
fi

DL=/var/cache/goboxd-dl
TC=/var/cache/goboxd-toolchains
mkdir -p "$DL" "$TC"

if [ ! -d "$TC/clojure" ]; then
    [ -f "$DL/clojure-tools.tar.gz" ] || curl -fsSL -o "$DL/clojure-tools.tar.gz" \
        https://download.clojure.org/install/clojure-tools-1.12.0.1530.tar.gz
    rm -rf "$TC/clojure"
    mkdir -p "$TC/clojure"
    tar -xzf "$DL/clojure-tools.tar.gz" -C "$TC/clojure" --strip-components=1
fi

# The tarball's launchers are templates with literal PREFIX/BINDIR placeholders
# and resolve the jars from $prefix/libexec/; the bundled install.sh needs ruby
# (HOMEBREW_RUBY_PATH), so replicate it with sed instead: same layout, same
# one-file code base, no extra runtime dependency.
rm -rf /usr/local/clojure
mkdir -p /usr/local/clojure/bin /usr/local/clojure/libexec
cp "$TC/clojure"/deps.edn "$TC/clojure"/example-deps.edn "$TC/clojure"/tools.edn /usr/local/clojure/
cp "$TC/clojure"/*.jar /usr/local/clojure/libexec/
sed -e "s|PREFIX|/usr/local/clojure|g" "$TC/clojure/clojure" > /usr/local/clojure/bin/clojure
sed -e "s|BINDIR|/usr/local/clojure/bin|g" "$TC/clojure/clj" > /usr/local/clojure/bin/clj
chmod +x /usr/local/clojure/bin/clojure /usr/local/clojure/bin/clj

ln -sf /usr/local/clojure/bin/clojure /usr/local/bin/clojure
ln -sf /usr/local/clojure/bin/clj /usr/local/bin/clj

if command -v clojure &> /dev/null; then
    echo "Clojure installation verified successfully."
    clojure --version
    printf '(println "clojure works:" (* 6 7))\n' > /tmp/smoke.clj
    clojure -M /tmp/smoke.clj
else
    echo "Clojure installation verification failed"
    exit 1
fi
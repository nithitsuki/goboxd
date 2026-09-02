# Step 1: Build nsjail and goboxd
# Base image digests are pinned for reproducible builds (scripts/check-pins.sh).
FROM golang:1.25-bookworm@sha256:c62765daa3fb92521a46cc8242797b81b03cf592b5aeffc36bae63d9abc1385c AS builder

# Install nsjail build dependencies (pinned; see scripts/check-pins.sh)
RUN apt-get update && apt-get install -y \
    build-essential=12.9 \
    pkg-config=1.8.1-1 \
    bison=2:3.8.2+dfsg-1+b1 \
    flex=2.6.4-8.2 \
    libprotobuf-dev=3.21.12-3+deb12u1 \
    protobuf-compiler=3.21.12-3+deb12u1 \
    libnl-route-3-dev=3.7.0-0.2+b1

# Build nsjail
COPY external/nsjail /nsjail-src
WORKDIR /nsjail-src
RUN make -j$(nproc)

# Build goboxd
WORKDIR /app
COPY go.mod ./
COPY . .
# Build-time metadata, stamped into the binary via -ldflags (see buildinfo).
# Docker contexts have no .git, so the commit must be passed explicitly;
# scripts/build.sh supplies the real HEAD SHA.
ARG COMMIT=dev
ARG VERSION=0.2.0
RUN CGO_ENABLED=0 GOOS=linux go build -o goboxd \
    -ldflags "-s -w \
      -X github.com/nithitsuki/goboxd/internal/buildinfo.Commit=${COMMIT} \
      -X github.com/nithitsuki/goboxd/internal/buildinfo.Version=${VERSION} \
      -X github.com/nithitsuki/goboxd/internal/buildinfo.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    ./cmd/goboxd

# Step 2a: Python 2 source (EOL runtime, not in Debian repos)
FROM python:2.7-slim@sha256:b68d40df862ac07e8955ea0fc0c5454cb4245b6165e79bc8ea2cc69170d9ba62 AS python2

# Step 2: Runtime environment with languages
FROM debian:bookworm-slim@sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143

# Build-time language selection. A comma-separated list of language ids, e.g.
# "py3,c,swift". Excluded languages install nothing (fast, small image).
# Default "all" installs every language.
ARG LANGS=all

# Copy scripts
COPY scripts/ /app/scripts/
WORKDIR /app

# Debian images ship /etc/apt/apt.conf.d/docker-clean, a DPkg::Post-Invoke
# hook that deletes downloaded .deb files after every install. With
# /var/cache/apt/archives mounted as a BuildKit cache mount, this hook
# empties the mount on every run and forces re-downloading. Remove it so
# cached .deb files survive across builds.
RUN rm -f /etc/apt/apt.conf.d/docker-clean

# Make all scripts executable
RUN chmod +x /app/scripts/lang_install/*.sh

# Cache mounts used by the language layers:
#   /var/cache/goboxd-dl           downloaded tarballs/installers
#   /var/cache/goboxd-toolchains   extracted toolchains (swift, dotnet, ...)
#   /var/lib/apt/lists             apt package indexes
#   /var/cache/apt/archives        downloaded .deb packages
# They persist across builds, so rebuilding a layer does not re-download or
# re-extract the compilers. `docker builder prune` clears them.
#
# The language id is matched as a comma-separated token (",py3,c," pattern).

# --- Layer 1: System dependencies ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    /app/scripts/lang_install/system.sh

# --- Layer 2: C ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",c,"; then /app/scripts/lang_install/c.sh; fi

# --- Layer 3: C++ ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",cpp,"; then /app/scripts/lang_install/cpp.sh; fi

# --- Layer 4: Python 3 ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",py3,"; then /app/scripts/lang_install/python3.sh; fi

# --- Layer 5: Java ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",java,"; then /app/scripts/lang_install/java.sh; fi

# --- Layer 6: Node.js ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",js,"; then /app/scripts/lang_install/nodejs.sh; fi

# --- Layer 7: Haskell ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",haskell,"; then /app/scripts/lang_install/haskell.sh; fi

# --- Layer 8: OCaml ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",ocaml,"; then /app/scripts/lang_install/ocaml.sh; fi

# --- Layer 9: R ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",r,"; then /app/scripts/lang_install/r.sh; fi

# --- Layer 10: D / GDC ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",d,"; then /app/scripts/lang_install/gdc.sh; fi

# --- Layer 11: LuaJIT ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",lua,"; then /app/scripts/lang_install/luajit.sh; fi

# --- Layer 12: Verilog ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",verilog,"; then /app/scripts/lang_install/iverilog.sh; fi

# --- Layer 13: Rust ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",rust,"; then /app/scripts/lang_install/rust.sh; fi

# --- Layer 14: Go ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",go,"; then /app/scripts/lang_install/go.sh; fi

# --- Layer 15: Erlang ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",erl,"; then /app/scripts/lang_install/erlang.sh; fi

# --- Layer 16: Lisp ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",lisp,"; then /app/scripts/lang_install/lisp.sh; fi

# --- Layer 17: Python 2 ---
# (unconditional: a ~200MB copy from the python:2.7-slim stage)
COPY --from=python2 /usr/local/bin/python2.7 /usr/local/bin/python2.7
COPY --from=python2 /usr/local/lib/python2.7 /usr/local/lib/python2.7
COPY --from=python2 /usr/local/lib/libpython2.7.so.1.0 /usr/local/lib/libpython2.7.so.1.0
RUN if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",py2,"; then \
      ldconfig && python2.7 --version && python2.7 -c "print('Python 2 is working correctly!')"; \
    else \
      rm -rf /usr/local/bin/python2.7 /usr/local/lib/python2.7 /usr/local/lib/libpython2.7.so.1.0; \
    fi

# --- Layer 18: PHP ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",php,"; then /app/scripts/lang_install/php.sh; fi

# --- Layer 19: Ruby ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",ruby,"; then /app/scripts/lang_install/ruby.sh; fi

# --- Layer 20: Elixir (requires the Erlang layer above) ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",elixir,"; then /app/scripts/lang_install/elixir.sh; fi

# --- Layer 21: TypeScript ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    --mount=type=cache,target=/var/cache/goboxd-npm \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",ts,"; then /app/scripts/lang_install/typescript.sh; fi

# --- Layer 22: Racket ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",racket,"; then /app/scripts/lang_install/racket.sh; fi

# --- Layer 23: .NET (C#) ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",csharp,"; then /app/scripts/lang_install/csharp.sh; fi

# --- Layer 24: Swift ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",swift,"; then /app/scripts/lang_install/swift.sh; fi

# --- Layer 25: Scala ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",scala,"; then /app/scripts/lang_install/scala.sh; fi

# --- Layer 26: Kotlin ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",kotlin,"; then /app/scripts/lang_install/kotlin.sh; fi

# --- Layer 27: Dart ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",dart,"; then /app/scripts/lang_install/dart.sh; fi

# --- Layer 28: Pascal ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",pascal,"; then /app/scripts/lang_install/pascal.sh; fi

# --- Language backlog layers (2026-09-02) ---

# --- Layer: Clojure (needs the java layer, installed above) ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",clojure,"; then /app/scripts/lang_install/clojure.sh; fi

# --- Layer: COBOL ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",cobol,"; then /app/scripts/lang_install/cobol.sh; fi

# --- Layer: CoffeeScript (needs the nodejs layer) ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",coffeescript,"; then /app/scripts/lang_install/coffeescript.sh; fi

# --- Layer: Crystal ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",crystal,"; then /app/scripts/lang_install/crystal.sh; fi

# --- Layer: Dash ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",dash,"; then /app/scripts/lang_install/dash.sh; fi

# --- Layer: .NET (Mono) ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",dotnet,"; then /app/scripts/lang_install/dotnet.sh; fi

# --- Layer: Emacs Lisp ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",elisp,"; then /app/scripts/lang_install/elisp.sh; fi

# --- Layer: Groovy (needs the java layer) ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",groovy,"; then /app/scripts/lang_install/groovy.sh; fi

# --- Layer: Julia ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",julia,"; then /app/scripts/lang_install/julia.sh; fi

# --- Layer: NASM ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",nasm,"; then /app/scripts/lang_install/nasm.sh; fi

# --- Layer: Nim ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",nim,"; then /app/scripts/lang_install/nim.sh; fi

# --- Layer: Octave ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",octave,"; then /app/scripts/lang_install/octave.sh; fi

# --- Layer: Odin ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",odin,"; then /app/scripts/lang_install/odin.sh; fi

# --- Layer: Pony ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",pony,"; then /app/scripts/lang_install/pony.sh; fi

# --- Layer: Prolog ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",prolog,"; then /app/scripts/lang_install/prolog.sh; fi

# --- Layer: PowerShell ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",pwsh,"; then /app/scripts/lang_install/pwsh.sh; fi

# --- Layer: Raku ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",raku,"; then /app/scripts/lang_install/raku.sh; fi

# --- Layer: GNU Smalltalk (source build) ---
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    --mount=type=cache,target=/var/cache/goboxd-dl \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",smalltalk,"; then /app/scripts/lang_install/smalltalk.sh; fi

# --- Layer: V ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",vlang,"; then /app/scripts/lang_install/vlang.sh; fi

# --- Layer: Zig ---
RUN --mount=type=cache,target=/var/cache/goboxd-dl \
    --mount=type=cache,target=/var/cache/goboxd-toolchains \
    if [ "$LANGS" = "all" ] || echo ",$LANGS," | grep -q ",zig,"; then /app/scripts/lang_install/zig.sh; fi

# --- Layer 29: Container egress firewall ---
# iptables for the entrypoint's OUTPUT firewall. Kept as its own layer so
# the heavy language layers stay cached.
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    apt-get update && apt-get install -y --no-install-recommends iptables=1.8.9-2

# Copy config
COPY config/ /app/config/

# Copy binaries from builder
COPY --from=builder /nsjail-src/nsjail /usr/bin/nsjail
COPY --from=builder /app/goboxd /usr/local/bin/goboxd

# Entrypoint applies the egress firewall (block all new outbound
# connections) before starting goboxd.
RUN chmod +x /app/scripts/entrypoint.sh
ENTRYPOINT ["/app/scripts/entrypoint.sh"]

EXPOSE 8080
CMD ["goboxd"]

# Step 1: Build nsjail and goboxd
FROM golang:1.25-bookworm AS builder

# Install nsjail build dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    pkg-config \
    bison \
    flex \
    libprotobuf-dev \
    protobuf-compiler \
    libnl-route-3-dev

# Build nsjail
COPY external/nsjail /nsjail-src
WORKDIR /nsjail-src
RUN make -j$(nproc)

# Build goboxd
WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o goboxd ./cmd/goboxd

# Step 2a: Python 2 source (EOL runtime, not in Debian repos)
FROM python:2.7-slim AS python2

# Step 2: Runtime environment with languages
FROM debian:bookworm-slim

# Build-time language selection. A comma-separated list of language ids, e.g.
# "py3,c,swift". Excluded languages install nothing (fast, small image).
# Default "all" installs every language.
ARG LANGS=all

# Copy scripts
COPY scripts/ /app/scripts/
WORKDIR /app

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

# --- Layer 29: Final cleanup ---
RUN apt clean && rm -rf /var/lib/apt/lists/*

# --- Layer 30: Container egress firewall ---
# iptables for the entrypoint's OUTPUT firewall. Kept as its own layer so
# the heavy language layers stay cached.
RUN --mount=type=cache,target=/var/lib/apt/lists \
    --mount=type=cache,target=/var/cache/apt/archives \
    apt-get update && apt-get install -y --no-install-recommends iptables \
    && apt clean && rm -rf /var/lib/apt/lists/*

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

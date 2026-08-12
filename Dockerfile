# Stage 1: Build nsjail and goboxd
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

# Stage 2a: Python 2 source (EOL runtime, not in Debian repos)
FROM python:2.7-slim AS python2

# Stage 2: Runtime environment with languages
FROM debian:bookworm-slim

# Copy scripts
COPY scripts/ /app/scripts/
WORKDIR /app

# Make all scripts executable
RUN chmod +x /app/scripts/lang_install/*.sh

# --- Layer 1: System dependencies ---
RUN /app/scripts/lang_install/system.sh

# --- Layer 2: C ---
RUN /app/scripts/lang_install/c.sh

# --- Layer 3: C++ ---
RUN /app/scripts/lang_install/cpp.sh

# --- Layer 4: Python 3 ---
RUN /app/scripts/lang_install/python3.sh

# --- Layer 5: Java ---
RUN /app/scripts/lang_install/java.sh

# --- Layer 6: Node.js ---
RUN /app/scripts/lang_install/nodejs.sh

# --- Layer 7: Haskell ---
RUN /app/scripts/lang_install/haskell.sh

# --- Layer 8: OCaml ---
RUN /app/scripts/lang_install/ocaml.sh

# --- Layer 9: R ---
RUN /app/scripts/lang_install/r.sh

# --- Layer 10: D / GDC ---
RUN /app/scripts/lang_install/gdc.sh

# --- Layer 11: LuaJIT ---
RUN /app/scripts/lang_install/luajit.sh

# --- Layer 12: Verilog ---
RUN /app/scripts/lang_install/iverilog.sh

# --- Layer 13: Rust ---
RUN /app/scripts/lang_install/rust.sh

# --- Layer 14: Go ---
RUN /app/scripts/lang_install/go.sh

# --- Layer 15: Erlang ---
RUN /app/scripts/lang_install/erlang.sh

# --- Layer 16: Lisp ---
RUN /app/scripts/lang_install/lisp.sh

# --- Layer 17: Python 2 ---
COPY --from=python2 /usr/local/bin/python2.7 /usr/local/bin/python2.7
COPY --from=python2 /usr/local/lib/python2.7 /usr/local/lib/python2.7
COPY --from=python2 /usr/local/lib/libpython2.7.so.1.0 /usr/local/lib/libpython2.7.so.1.0
RUN ldconfig && python2.7 --version && python2.7 -c "print('Python 2 is working correctly!')"

# --- Layer 18: PHP ---
RUN /app/scripts/lang_install/php.sh

# --- Layer 19: Ruby ---
RUN /app/scripts/lang_install/ruby.sh

# --- Layer 20: Elixir (requires the Erlang layer above) ---
RUN /app/scripts/lang_install/elixir.sh

# --- Layer 21: TypeScript ---
RUN /app/scripts/lang_install/typescript.sh

# --- Layer 22: Racket ---
RUN /app/scripts/lang_install/racket.sh

# --- Layer 23: .NET (C#) ---
RUN /app/scripts/lang_install/csharp.sh

# --- Layer 24: Swift ---
RUN /app/scripts/lang_install/swift.sh

# --- Layer 25: Scala ---
RUN /app/scripts/lang_install/scala.sh

# --- Layer 26: Kotlin ---
RUN /app/scripts/lang_install/kotlin.sh

# --- Layer 27: Dart ---
RUN /app/scripts/lang_install/dart.sh

# --- Layer 28: Final cleanup ---
RUN apt clean && rm -rf /var/lib/apt/lists/*

# Copy config
COPY config/ /app/config/

# Copy binaries from builder
COPY --from=builder /nsjail-src/nsjail /usr/bin/nsjail
COPY --from=builder /app/goboxd /usr/local/bin/goboxd

EXPOSE 8080
CMD ["goboxd"]

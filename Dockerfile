# Stage 1: Build nsjail and goboxd
FROM golang:1.23-bookworm AS builder

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

# Stage 2: Runtime environment with languages
FROM debian:bookworm-slim

# Copy config, install scripts, and set up the runtime
COPY config/ /app/config/
COPY scripts/ /app/scripts/
WORKDIR /app

# Run the host dependencies and language installations
RUN chmod +x /app/scripts/install.sh && /app/scripts/install.sh

# Copy binaries from builder
COPY --from=builder /nsjail-src/nsjail /usr/bin/nsjail
COPY --from=builder /app/goboxd /usr/local/bin/goboxd

EXPOSE 8080
CMD ["goboxd"]

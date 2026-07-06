#!/bin/bash
# Benchmark goboxd under load using hey.
# Standalone tool, not part of goboxd itself.
# Usage: ./scripts/bench.sh [url]
# Default url: http://localhost:8080

set -e

URL="${1:-http://localhost:8080}"
HEY="${HEY:-$(which hey 2>/dev/null || echo ~/go/bin/hey)}"
CONCURRENCY_LEVELS="${CONCURRENCY_LEVELS:-"1 10 25 50 75 100 150 200"}"

if [ ! -x "$HEY" ]; then
    echo "hey not found. Install with: go install github.com/rakyll/hey@latest"
    exit 1
fi

echo "=== goboxd benchmark ==="
echo "Target: $URL"
echo "Date:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo ""

if ! curl -sf "$URL/healthz" > /dev/null 2>&1; then
    echo "Server at $URL is not responding to /healthz"
    echo "Start it with: docker compose up -d"
    exit 1
fi

echo "Server is healthy, starting benchmarks..."
echo ""

RESULTS_FILE=""
if [ "$BENCH_SAVE" = "1" ]; then
    RESULTS_FILE="$(dirname "$0")/../docs/benchmarks.md"
    {
        echo ""
        echo "### $(date -u +%Y-%m-%d) -- $(uname -m)"
        echo ""
        echo "| Clients | Requests/s | p50 (ms) | p95 (ms) | p99 (ms) | Total reqs |"
        echo "|---|---|---|---|---|---|"
    } >> "$RESULTS_FILE"
fi

PAYLOAD='{"language":"py3","source":"print(42)","tests":[{"stdin":"","expected_stdout":"42\n"}]}'

for c in $CONCURRENCY_LEVELS; do
    echo "--- Concurrency: $c ---"

    RAW=$("$HEY" -c "$c" -n 1000 -m POST \
        -H "Content-Type: application/json" \
        -d "$PAYLOAD" \
        "$URL/run" 2>&1)

    RPS=$(echo "$RAW" | grep "Requests/sec" | awk '{print $2}')

    # hey outputs latency as "50%% in 0.0006 secs"
    P50=$(echo "$RAW" | grep "50%%" | awk '{print $3}')
    P95=$(echo "$RAW" | grep "95%%" | awk '{print $3}')
    P99=$(echo "$RAW" | grep "99%%" | awk '{print $3}')

    # Convert seconds to milliseconds using awk
    P50_MS=$(awk -v s="$P50" 'BEGIN { printf "%.2f", s * 1000 }')
    P95_MS=$(awk -v s="$P95" 'BEGIN { printf "%.2f", s * 1000 }')
    P99_MS=$(awk -v s="$P99" 'BEGIN { printf "%.2f", s * 1000 }')

    echo "  Requests/sec: $RPS"
    echo "  p50: ${P50_MS}ms  p95: ${P95_MS}ms  p99: ${P99_MS}ms"
    echo ""

    if [ -n "$RESULTS_FILE" ]; then
        echo "| $c | $RPS | $P50_MS | $P95_MS | $P99_MS | 1000 |" >> "$RESULTS_FILE"
    fi
done

echo "=== Benchmark complete ==="
if [ -n "$RESULTS_FILE" ]; then
    echo "Results saved to $RESULTS_FILE"
fi

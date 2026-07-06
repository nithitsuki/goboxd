#!/bin/bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS="$DIR/../docs/loadtest"
mkdir -p "$RESULTS"

# Build the request body (150 MB MemoryHog)
cat > /tmp/run-request.json << 'JSON'
{"language":"java","source":"public class Main {\n    public static void main(String[] args) throws InterruptedException {\n        final int megabytes = 150;\n        final int blockSize = 1 << 20;\n        final byte[][] blocks = new byte[megabytes][];\n        long checksum = 0;\n        for (int i = 0; i < megabytes; i++) {\n            byte[] block = new byte[blockSize];\n            for (int j = 0; j < blockSize; j += 4096) {\n                block[j] = (byte) (i * 31 + j);\n                checksum += block[j];\n            }\n            blocks[i] = block;\n        }\n        for (byte[] block : blocks) {\n            for (int j = 0; j < block.length; j += 512) {\n                checksum += block[j];\n            }\n        }\n        Thread.sleep(1000);\n        System.out.println(\"MemoryHog OK mb=\" + megabytes + \" checksum=\" + checksum);\n    }\n}","tests":[{"stdin":"","expected_stdout":""}]}
JSON

# Vegeta target file
cat > /tmp/target.txt << 'TARGET'
POST http://localhost:8080/run
Content-Type: application/json
@/tmp/run-request.json
TARGET

# CSV header
echo "target_rps,throughput_rps,duration_s,requests,success,failed,error_pct,p50_ms,p95_ms,p99_ms,max_ms" > "$RESULTS/results.csv"

# Rate ladder — rise until first failure, then go a few steps past
# With 2 vCPU / 2GB RAM, expect breaking point around 4-5 RPS
RATES="1 2 3 4 5 6 8 10"

for rate in $RATES; do
  echo "=== Running at $rate RPS for 30s ==="
  vegeta attack -rate="${rate}/1s" -duration=30s -timeout=10s -targets=/tmp/target.txt \
    | vegeta report -type=json > "/tmp/report-${rate}.json"

  jq -r --arg r "$rate" '
    [ $r,
      .throughput,
      (.duration/1e9),
      .requests,
      (.status_codes["200"] // 0),
      (.requests - (.status_codes["200"] // 0)),
      ((1 - .success) * 100),
      (.latencies["50th"]/1e6),
      (.latencies["95th"]/1e6),
      (.latencies["99th"]/1e6),
      (.latencies.max/1e6)
    ] | @csv' "/tmp/report-${rate}.json" >> "$RESULTS/results.csv"
done

echo "Done. Results in $RESULTS/results.csv"

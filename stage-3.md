# Stage 3 — Load testing goboxd

This morning we proved the sandbox can run many languages. This afternoon we find out how many requests it can run at once before it falls over.

We hand every team the same Java program. It is a memory-heavy workload: each run allocates a large block of heap, touches it, does a little compute, and holds it for a moment. One run is harmless. A hundred runs landing at the same time is a different story. Your job is to push your own goboxd service with this program under rising concurrent load, find the exact point where it starts failing, and show us the curve.

---

## Plan

We'll tackle this in small steps:

| Step | What |
|---|---|
| **Step 1** | Run the Java program once through goboxd, add MemoryHog preset to playground |
| **Step 2** | Add a load-test runner script (vegeta) that drives concurrent requests |
| **Step 3** | Run the ladder — hold each RPS for 30s, record results |
| **Step 4** | Plot the graphs (breaking-point + latency) |
| **Step 5** | Write up `docs/loadtest/README.md` |

---

## Setup

### Container resource limits (`docker-compose.yml`)

```yaml
services:
  goboxd:
    build: .
    ports:
      - "8080:8080"
    privileged: true
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: "2"
          memory: 2G
    volumes:
      - ./tests/testcases:/app/testcases:ro
    environment:
      - PORT=8080
```

- **2 vCPU**, **2 GB RAM**
- Per-request timeout: **10 seconds**

### The Java program (`MemoryHog.java`)

See bottom of this page for the full source. The program:

1. Reads `MEMHOG_MB` env var (default 150 MB)
2. Allocates that many 1 MB blocks
3. Touches every 4 KB page (forces RSS commitment)
4. Does a light CPU pass (touches every 512th byte)
5. Sleeps 1 second (holds memory so concurrent runs pile up)
6. Prints a checksum

### Load-testing tool: **vegeta**

```bash
# Install
brew install vegeta  # or: go install github.com/tsenart/vegeta/v12@latest
```

---

## Step 1 — Run the Java program

### Verify a single run

```bash
curl -s -X POST http://localhost:8080/run \
  -H "Content-Type: application/json" \
  -d '{
    "language": "java",
    "source": "public class Main {\n    public static void main(String[] args) throws InterruptedException {\n        final int megabytes = 10;\n        final int blockSize = 1 << 20;\n        final byte[][] blocks = new byte[megabytes][];\n        long checksum = 0;\n        for (int i = 0; i < megabytes; i++) {\n            byte[] block = new byte[blockSize];\n            for (int j = 0; j < blockSize; j += 4096) {\n                block[j] = (byte) (i * 31 + j);\n                checksum += block[j];\n            }\n            blocks[i] = block;\n        }\n        for (byte[] block : blocks) {\n            for (int j = 0; j < block.length; j += 512) {\n                checksum += block[j];\n            }\n        }\n        Thread.sleep(100);\n        System.out.println(\"MemoryHog OK mb=\" + megabytes + \" checksum=\" + checksum);\n    }\n}",
    "tests": [{"stdin": "", "expected_stdout": ""}]
  }' | jq .
```

Expected response: `{"status":"accepted","tests":[{"stdout":"MemoryHog OK mb=10 checksum=58880\n"}]}`

### Playground preset

The MemoryHog program is already added as a preset in the playground under **Java → MemoryHog 10MB**. Open http://localhost:8080/playground, select **Java**, then pick **MemoryHog 10MB** from the presets dropdown and click **run**.

---

## Step 2 — Load-test script

Create `scripts/loadtest.sh`:

```bash
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

# Rate ladder
RATES="5 10 25 50 75 100 150 200 300 400"

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
```

Make it executable:

```bash
chmod +x scripts/loadtest.sh
```

---

## Step 3 — Run the load test

```bash
# Ensure the container is running with 2 vCPU / 2 GB limits
docker compose up -d

# Run the load test
./scripts/loadtest.sh
```

If the service breaks early (errors appear at 25 RPS), add a few extra steps after the break to show the curve shape (e.g. 30, 35, 40).

---

## Step 4 — Plot the graphs

Create `docs/loadtest/plot.py`:

```python
import csv
import matplotlib.pyplot as plt

rows = list(csv.DictReader(open("docs/loadtest/results.csv")))
rps  = [float(r["target_rps"]) for r in rows]

# --- Breaking-point graph ---
plt.figure()
plt.plot(rps, [float(r["error_pct"]) for r in rows], marker="o", color="crimson")
plt.xlabel("Offered RPS")
plt.ylabel("Error rate (%)")
plt.title("Breaking point")
plt.axhline(y=0, color="gray", linestyle="--", alpha=0.5)
# Find and mark the breaking point
for r in rows:
    if float(r["error_pct"]) > 0:
        bp_rps = float(r["target_rps"])
        plt.axvline(x=bp_rps, color="orange", linestyle=":", label=f"Break at {bp_rps} RPS")
        break
plt.legend()
plt.savefig("docs/loadtest/breaking-point.png", dpi=150, bbox_inches="tight")
print("Saved breaking-point.png")

# --- Latency graph ---
plt.figure()
for k, lbl in [("p50_ms", "p50"), ("p95_ms", "p95"), ("p99_ms", "p99")]:
    plt.plot(rps, [float(r[k]) for r in rows], marker="o", label=lbl)
plt.xlabel("Offered RPS")
plt.ylabel("Latency (ms)")
plt.title("RPS vs latency")
plt.legend()
plt.savefig("docs/loadtest/latency.png", dpi=150, bbox_inches="tight")
print("Saved latency.png")
```

Run it:

```bash
python3 docs/loadtest/plot.py
```

---

## Step 5 — Write up

Create `docs/loadtest/README.md` with:

- Container limits used (2 vCPU, 2 GB RAM)
- Load tool and version (vegeta)
- Breaking-point RPS and what failed first (memory, concurrency limiter, GC, queue rejection)
- How to reproduce
- Whether degradation was graceful or hard-crash

---

## Submission checklist

```
docs/loadtest/
├── README.md
├── results.csv
├── breaking-point.png
├── latency.png
├── plot.py
scripts/loadtest.sh
```

Everything committed to the team branch.

---

## Scoring

| Component | Points |
|---|---|
| Reproducible run: container limits documented, load-test script committed | 10 |
| Breaking point correctly identified and matching the CSV | 15 |
| Both graphs correct and consistent with the CSV | 15 |
| Graceful degradation under overload | 10 |
| Live demo: clear walkthrough and correct failure mode | 10 |

---

## The program (MemoryHog.java)

```java
public class MemoryHog {
    public static void main(String[] args) throws InterruptedException {
        final int megabytes = readSize();
        final int blockSize = 1 << 20; // 1 MB per block
        final byte[][] blocks = new byte[megabytes][];

        long checksum = 0;

        // Allocate and touch every page so the pages are actually committed to RSS.
        for (int i = 0; i < megabytes; i++) {
            byte[] block = new byte[blockSize];
            for (int j = 0; j < blockSize; j += 4096) {
                block[j] = (byte) (i * 31 + j);
                checksum += block[j];
            }
            blocks[i] = block;
        }

        // Light CPU pass so a run is not pure allocation.
        for (byte[] block : blocks) {
            for (int j = 0; j < block.length; j += 512) {
                checksum += block[j];
            }
        }

        // Hold the memory resident for a moment so concurrent runs pile up.
        Thread.sleep(1000);

        // Deterministic output proves the run actually completed.
        System.out.println("MemoryHog OK mb=" + megabytes + " checksum=" + checksum);
    }

    private static int readSize() {
        String env = System.getenv("MEMHOG_MB");
        if (env != null && !env.isEmpty()) {
            try {
                return Integer.parseInt(env.trim());
            } catch (NumberFormatException ignored) {
            }
        }
        return 150;
    }
}
```

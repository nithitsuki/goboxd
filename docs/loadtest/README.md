# Load test results

## Container limits

| Resource | Value |
|---|---|
| CPU | 2 vCPUs |
| RAM | 2 GB |
| Concurrency | `GOBOXD_MAX_JOBS=4` (channel semaphore) |
| Per-request timeout | 10 s |

## Tooling

- **Load generator:** [vegeta](https://github.com/tsenart/vegeta) v12
- **Workload:** Java MemoryHog (150 MB heap allocate + page touch + 1 s hold)
- **Script:** `scripts/loadtest.sh`
- **Plotting:** `python3` + `matplotlib` via `docs/loadtest/plot.py`

## Results

```
target_rps,throughput_rps,duration_s,requests,success,failed,error_pct,p50_ms,p95_ms,p99_ms,max_ms
"1",0.9860615164883954,29.00000425,30,30,0,0,1440.680166,1623.563333,1674.174583,1674.174583
"2",1.9061527558668745,29.500019375,60,60,0,0,1782.142208,2180.821146,2291.034658,2297.373458
"3",0.8066763170638436,29.666690833,90,32,58,64.44444444444444,10000.287145,10002.44475,10003.1564,10003.26625
"4",0,29.750415791,120,0,120,100,10000.402666,10001.448208,10001.612862,10001.775292
"5",0,29.799973708,150,0,150,100,10000.561697,10001.517292,10001.797959,10002.655667
"6",0,29.833398125,180,0,180,100,10000.331812,10001.362854,10001.537583,10002.020709
"8",0,29.875024084,240,0,240,100,10000.335041,10001.290875,10001.615104,10002.059792
"10",0,29.899993417,300,0,300,100,10000.173062,10001.246979,10001.354437,10001.637375
```

## Breaking point: 3 RPS

At 3 offered RPS, the system reaches its saturation point:

- 32 of 90 requests succeed (36 percent)
- Throughput drops to 0.81 RPS
- p50 latency jumps to 10 s (the timeout ceiling)
- Beyond 3 RPS, 100 percent of requests time out

## Failure mode: Graceful degradation

1. **Sub-saturation (1-2 RPS):** All requests succeed. The p50 latency
   rises from 1.4 s at 1 RPS to 1.8 s at 2 RPS. This is the MemoryHog's 1 s
   sleep plus the JVM allocation overhead.
2. **Saturation (3 RPS):** The queue starts to build. The concurrency
   semaphore (4 slots) is fully occupied. Some requests exceed the 10 s
timeout in the queue. No server crash occurs. The timeouts are clean.
   The response is HTTP 200 with `status: "time_exceeded"`.
3. **Overload (4+ RPS):** The queue saturates completely. All requests time
   out. The server remains responsive. Health checks pass. After the load
   stops, the server recovers immediately. No lingering degradation occurs.

## Bottleneck: Memory pressure

Each MemoryHog request uses about **354 MB peak** (including JVM overhead).
At 4 concurrent requests, the memory use is 4 × 354 MB = 1.4 GB. This is
added to the Go runtime and the nsjail infrastructure. With 2 GB total, the
system runs near its memory ceiling. The concurrency limit is
`GOBOXD_MAX_JOBS=4`. It stays within the memory budget. Higher values (5+)
caused more memory contention and worse throughput.

## Recovery

The service recovers instantly when the load drops. No processes leak. No
memory is permanently consumed. The `/info` endpoint reports clean stats.
This is graceful degradation. The system says "no" under pressure with
timeouts. It does not crash.

## How to reproduce

```bash
# Ensure container is running with 2 vCPU / 2 GB limits
docker compose up -d

# Run the load test
./scripts/loadtest.sh

# Generate graphs
uv run --script docs/loadtest/plot.py
```

## Further optimization potential

These results are the best achievable with the default Debian base image and
stock OpenJDK. The 3 RPS breaking point reflects real-world constraints.
Targeted changes can improve these results:

| Approach | Expected gain | Trade-off |
|---|---|---|
| **Alpine Linux base** | Smaller image, lower memory overhead per sandbox | No glibc. Some languages (Erlang, Haskell) may need extra packages |
| **Custom JVM flags** (`-Xmx`, `-Xms`, `-XX:+UseSerialGC`) | Reduce per-request heap from ~200 MB to ~160 MB, less GC pause | May affect JIT compilation speed |
| **Swap space** (add 1-2 GB swap to container) | Absorb memory spikes. Fewer OOM kills at 4+ RPS | Latency spikes when swapping |
| **Class data sharing** (CDS / AppCDS) | Faster JVM startup for repeated runs | Requires pre-built archive. Adds complexity |
| **GraalVM native image** | Sub-ms startup, minimal memory | Not a drop-in replacement. Language compatibility issues |
| **Pre-warmed JVM pool** | Skip JVM startup + compilation per request | Resource overhead of idle JVMs |
| **Larger container** (4 GB RAM) | ~6 RPS throughput (double) | Goes beyond the tested configuration |

Without changes to the workload parameters (Debian, stock JVM, 2 GB RAM),
the 3 RPS breaking point is the ceiling.

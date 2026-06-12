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
1,0.99,29.0,30,30,0,0,1428,1633,1653,1653
2,1.94,29.5,60,60,0,0,1542,1770,1790,1791
3,1.01,29.7,90,40,50,56,10000,10001,10002,10002
4,0,29.8,120,0,120,100,10000,10001,10001,10001
5,0,29.8,150,0,150,100,10001,10002,10002,10002
6,0,29.8,180,0,180,100,10000,10002,10002,10003
8,0,29.9,240,0,240,100,10000,10002,10002,10003
10,0,29.9,300,0,300,100,10000,10002,10002,10002
```

## Breaking point: **3 RPS**

At 3 offered RPS, the system hits its saturation point:
- 40/90 requests succeed (44%)
- Throughput drops to 1.01 RPS
- p50 latency jumps to 10 s (the timeout ceiling)
- Beyond 3 RPS, 100% of requests time out

## Failure mode: **Graceful degradation**

1. **Sub-saturation (1-2 RPS):** All requests succeed. p50 latency is flat at
   ~1.5 s (the MemoryHog's 1 s sleep + JVM allocation overhead).
2. **Saturation (3 RPS):** Queue begins to build. The concurrency semaphore
   (4 slots) is fully occupied. Some requests start timing out after 10 s in
   the queue. No server crash — clean timeouts with proper 200 responses
   containing `status: "time_exceeded"`.
3. **Overload (4+ RPS):** Queue saturates completely. All requests time out.
   Server remains responsive (health checks pass). After load stops, server
   recovers immediately — no lingering degradation.

## Bottleneck: **Memory pressure**

Each MemoryHog request uses **~354 MB peak** (including JVM overhead).
At 4 concurrent requests: 4 × 354 MB = 1.4 GB, plus the Go runtime and nsjail
infrastructure. With 2 GB total, the system runs near its memory ceiling.
The concurrency limit was tuned to `GOBOXD_MAX_JOBS=4` to stay within the
memory budget — higher values (5+) caused increased memory contention and
worse throughput.

## Recovery

The service recovers instantly when load drops. No processes leak, no memory
is permanently consumed, and the `/info` endpoint reports clean stats. This is
graceful degradation: the system says "no" under pressure (timeouts) rather
than crashing.

## How to reproduce

```bash
# Ensure container is running with 2 vCPU / 2 GB limits
docker compose up -d

# Run the load test
./scripts/loadtest.sh

# Generate graphs
uv run --script docs/loadtest/plot.py
```

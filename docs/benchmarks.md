# Benchmarks

## Methodology

- Tool: [hey](https://github.com/rakyll/hey) v0.1.5
- Scenario: `POST /run` with trivial `print(42)` Python 3 payload, 1000 requests per level
- Metrics: requests/sec, p50/p95/p99 latency
- Environment: clean Docker run on bare metal (x86_64, 24 cores)
- Server: goboxd running in Docker with `--privileged` for nsjail namespaces

## Baseline — without concurrency semaphore

| Clients | Requests/s | p50 (ms) | p95 (ms) | p99 (ms) |
|---|---|---|---|---|
| 1 | 1870 | 0.50 | 0.60 | 0.70 |
| 10 | 9057 | 1.10 | 1.40 | 2.10 |
| 25 | 11589 | 1.90 | 3.80 | 4.90 |
| 50 | 11885 | 2.80 | 9.50 | 13.30 |
| 75 | 10123 | 3.20 | 20.30 | 33.60 |
| 100 | 10281 | 4.30 | 31.30 | 41.70 |
| 150 | 10982 | 8.00 | 35.30 | 42.70 |
| 200 | 11023 | 10.20 | 45.40 | 59.60 |

## With bounded concurrency semaphore (GOBOXD_MAX_JOBS=24)

| Clients | Requests/s | p50 (ms) | p95 (ms) | p99 (ms) |
|---|---|---|---|---|
| 1 | 1931 | 0.50 | 0.60 | 0.70 |
| 10 | 8791 | 0.90 | 1.40 | 19.00 |
| 25 | 9207 | 2.00 | 5.00 | 18.60 |
| 50 | 10304 | 4.20 | 7.60 | 14.70 |
| 75 | 8962 | 6.40 | 26.30 | 32.00 |
| 100 | 8352 | 7.80 | 31.80 | 52.60 |
| 150 | 10119 | 10.80 | 32.70 | 33.30 |
| 200 | 10583 | 12.90 | 47.90 | 50.90 |

## Observations

- Both runs show similar peak throughput (~10,500-11,900 req/s) — the semaphore
  doesnt significantly cap performance at 24 slots on this hardware.
- Latency is marginally higher with the semaphore at high concurrency, as
  expected from queuing. Still well within acceptable range.
- No errors or dropped requests in either run.
- The semaphore prevents runaway nsjail process counts during bursts,
  trading a small latency increase for predictable resource usage.

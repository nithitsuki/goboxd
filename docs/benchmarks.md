# Benchmarks

## Python trivial payload (baseline)

- **Tool:** [hey](https://github.com/rakyll/hey) v0.1.5
- **Scenario:** `POST /run` with `print(42)` Python 3 payload, 1000 requests per level
- **Metrics:** requests/sec, p50/p95/p99 latency
- **Server:** goboxd in Docker, 24-core host

| Clients | Requests/s | p50 (ms) | p95 (ms) | p99 (ms) |
|---|---|---|---|---|
| 1 | 1870 | 0.50 | 0.60 | 0.70 |
| 10 | 9057 | 1.10 | 1.40 | 2.10 |
| 50 | 11885 | 2.80 | 9.50 | 13.30 |
| 100 | 10281 | 4.30 | 31.30 | 41.70 |
| 200 | 11023 | 10.20 | 45.40 | 59.60 |

## MemoryHog Java workload (challenge spec)

- **Tool:** [vegeta](https://github.com/tsenart/vegeta) v12
- **Scenario:** `POST /run` with 150 MB MemoryHog Java program, 30 s steady-state steps
- **Container limits:** 2 vCPU, 2 GB RAM, `GOBOXD_MAX_JOBS=4`
- **Client timeout:** 10 s per request

| Offered RPS | Success | Throughput | p50 (ms) | p95 (ms) |
|---|---|---|---|---|
| 1 | 100% | 0.99 RPS | 1428 | 1633 |
| 2 | 100% | 1.94 RPS | 1542 | 1770 |
| **3** | **44%** | **1.01 RPS** | **10000** | **10001** |
| 4 | 0% | 0 RPS | 10000 | 10001 |
| 5+ | 0% | 0 RPS | - | - |

**Breaking point: 3 RPS.** Bottleneck is memory pressure (354 MB per request x 4 concurrent = 1.4 GB in a 2 GB container). The service degrades gracefully with clean timeouts, no crashes.

See `docs/loadtest/` for full CSV, graphs, and analysis.

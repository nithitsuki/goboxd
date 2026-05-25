# Benchmarks

## Methodology

- Tool: [hey](https://github.com/rakyll/hey) v0.1.5
- Scenario: `POST /run` with trivial `print(42)` Python 3 payload, 1000 requests per level
- Metrics: requests/sec, p50/p95/p99 latency
- Environment: clean Docker run on bare metal (x86_64)
- Server: goboxd running in Docker with `--privileged` for nsjail namespaces

## Results

### 2026-05-31 — x86_64

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

## Observations

- Throughput peaks around 50 concurrent clients (~11,900 req/s) and stays flat through 200.
- Latency p50 stays under 10ms even at 200 concurrency.
- p95/p99 show the nsjail sandbox overhead varies: at low concurrency most requests complete in <1ms, but tail latency grows to ~60ms at 200 clients.
- No errors, no dropped requests at any level — nsjail handles the load without crashing.

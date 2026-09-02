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

## MemoryHog Java workload

- **Tool:** [vegeta](https://github.com/tsenart/vegeta) v12
- **Scenario:** `POST /run` with 150 MB MemoryHog Java program, 30 s steady-state steps
- **Container limits:** 2 vCPU, 2 GB RAM, `GOBOXD_MAX_JOBS=4`
- **Client timeout:** 10 s per request

| Offered RPS | Success | Throughput | p50 (ms) | p95 (ms) |
|---|---|---|---|---|
| 1 | 100% | 0.99 RPS | 1441 | 1624 |
| 2 | 100% | 1.91 RPS | 1782 | 2181 |
| **3** | **36%** | **0.81 RPS** | **10000** | **10001** |
| 4 | 0% | 0 RPS | 10000 | 10001 |
| 5+ | 0% | 0 RPS | - | - |

**Breaking point: 3 RPS.** The bottleneck is memory pressure. Each request
uses 354 MB. Four concurrent requests use 1.4 GB in a 2 GB container. The
service degrades gracefully. It returns clean timeouts. No crashes occur.

See `docs/loadtest/` for the full CSV, graphs, and analysis.

## SLOs and regression gates

"Fast" needs numbers we can defend, so the following are treated as the
performance contract. They are enforced by the integration suite, not just
documented:

| Target | Baseline | Gate |
|---|---|---|
| Jail setup p50 | < 50 ms (Python trivial, single client) | soak loop per-request latency p50 (`TestSoakNoLeaks`) |
| Sustained throughput | ~11–12k RPS (Python trivial, 50 clients) | load job trend (`make load-save`), not a hard fail |
| Soak stability | 0 leaked jail dirs / cgroup leaves / fds / goroutines over N runs | `TestSoakNoLeaks` |
| Parser robustness | every payload returns a valid JSON envelope, never a crash | `FuzzRunParsing` |
| Per-core run rate | runs/sec, Python trivial | `BenchmarkRunThroughput` (`make bench`) |
| Graceful overload | clean 503 + `queue_full` at the cap, no crashes | `TestShutdown*` + admission tests |

The soak, fuzz, and benchmark gates run against a real nsjail server via
`make integration` (harness-spawned) or `make integration-docker` (against a
running container). The soak defaults to 200 runs and accepts
`GOBOXD_SOAK_ITERATIONS` (for example `GOBOXD_SOAK_ITERATIONS=1000`) for
deeper leak runs; its per-request latency p50 is asserted against the
jail-setup SLO above, and it fails on any leaked jail dir, dangling cgroup
leaf, or fd/goroutine growth in the server process. `FuzzRunParsing` feeds
malformed and well-formed `POST /run` payloads and fails on any panic or
non-JSON response. `BenchmarkRunThroughput` reports the per-core Python
trivial run rate (`go test -bench=BenchmarkRunThroughput -benchtime=5s
./tests/integration/`).

To extend the load baseline: `make load` (hey) and `make load-save`
(vegeta, CSV + graphs under `docs/loadtest/`). The Python trivial baseline
above is the floor; any change that drops sustained RPS below ~9k at 50
clients warrants a look.

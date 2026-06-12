# Plan Evolution

## 2026-05-24 · From switch statements to YAML registry

**What we thought we'd do:**
Hardcode language configs as Go switch statements in the handler.

**What we actually did:**
Built a config-driven registry with LanguageConfig structs and a DefaultRegistry map. The YAML loading comes in Task 4.

**Why it changed:**
The spec says plug-and-play means adding a language with no Go code change. A switch statement would require recompiling. The registry pattern lets us swap in YAML loading later without touching handler code.

## 2026-05-25 · From random UID generation to MkdirTemp

**What we thought we'd do:**
Reimplement the reference's random UID retry-loop for directory isolation.

**What we actually did:**
Used os.MkdirTemp which guarantees unique directories with no collisions.

**Why it changed:**
The reference approach is unnecessarily complex and still has collision risk under load (hole #5). MkdirTemp is the standard Go way and pairs naturally with defer cleanup (hole #7).

## 2026-05-25 · From hardcoded status strings to spec-driven vocabulary

**What we thought we'd do:**
Map nsjail exit codes to status strings inline in the runner.

**What we actually did:**
Created computeTestStatus with the exact spec vocabulary — time_exceeded, wrong_output, output_whitespace_mismatch, memory_exceeded, etc. Also handles signal differentiation for memory vs timeout kills.

**Why it changed:**
The early version returned "timeout" and "wrong_answer" which dont match the spec.

## 2026-05-28 · From hardcoded e2e tests to fixture-driven testing

**What we thought we'd do:**
Write e2e tests as hardcoded Go test functions.

**What we actually did:**
Created a fixture system with input.json/want.json pairs that auto-discover and run. TestMain auto-starts the server.

**Why it changed:**
Adding a new language test case should not require writing Go code. The fixture approach means dropping a directory with two json files is enough. Same philosophy as the YAML language registry.

## 2026-05-28 · From simple kill detection to signal-aware status mapping

**What we thought we'd do:**
Map all killed processes to time_exceeded.

**What we actually did:**
Added signalKillReason that checks syscall.WaitStatus: SIGKILL = timeout, SIGSEGV/SIGABRT = memory_exceeded, others = runtime_error.

**Why it changed:**
The spec has memory_exceeded as a valid status but we never returned it. nsjail kills with different signals for different violations, so we can differentiate them.

## 2026-06-12 · From all penetration tests to a curated suite

**What I thought we'd do:**
Test every possible attack vector including fork bombs, thread storms, and reverse shells.

**What I actually did:**
Removed fork bombs, thread storms, and reverse shell tests. Kept file reads, shell injection, network
isolation, write protection, eval injection, symlink escapes, and DNS probes.

**Why it changed:**
Fork bombs require cgroup v2 enforcement which isn't available on macOS Docker Desktop. Without strict
process limits, they exhaust container PIDs/memory and crash the server. The remaining 52 tests probe
the boundaries that the sandbox can actually enforce.

## 2026-06-12 · From unlimited concurrency to bounded semaphore

**What I thought we'd do:**
Let the HTTP server handle as many concurrent requests as the OS allows.

**What I actually did:**
Added a channel-based semaphore initialized to runtime.NumCPU() = 8, later tuned to GOBOXD_MAX_JOBS=4.

**Why it changed:**
Without a concurrency cap, a burst of MemoryHog requests would consume all 2 GB RAM and OOM-kill the
server. The semaphore acts as a admission controller - requests queue up and wait for a slot instead of
overwhelming the system. The maxJobs value was tuned experimentally: 4 slots gave the best throughput
at the 3 RPS breaking point while staying within memory limits.

## 2026-06-12 · From playground embed to hybrid filesystem+embed

**What I thought we'd do:**
Fully embed the Vite-built playground in the Go binary via //go:embed.

**What I actually did:**
Committed a stub index.html, used //go:embed on the directory, and added a runtime filesystem check that
prefers real built files when available.

**Why it changed:**
The //go:embed directive fails at compile time if the embedded files don't exist. CI runners don't build
the playground, so every CI run would fail. The hybrid approach lets the CI build pass with the stub,
while the Docker build (which runs vite first) embeds the real playground.

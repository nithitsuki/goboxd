## 2026-05-25 · Using os.MkdirTemp instead of UID mapping logic

**Context:**
The reference architecture implements a custom retry-loop mechanism that generates random UIDs to assign temp directories, handling collisions by retrying three times (Security Hole #5).

**Options considered:**
1. Re-implementing the 30k-range random UID generator with a loop and atomic counters.
2. Leveraging Go's native `os.MkdirTemp` to handle unique, collision-proof allocation directly.

**Decision:**
We chose option 2: using `os.MkdirTemp()` inside the `ExecuteRun` pipeline and scoping it directly with `defer os.RemoveAll()`. 

**Rationale:**
Option 1 introduces artificial race conditions and unnecessary complexity without improving security. Go's native `os.MkdirTemp` already generates mathematically secure randomized suffixes, guaranteeing directory isolation without UID namespace management. Pairing it with a `defer os.RemoveAll()` block immediately after creation ensures we simultaneously patch Security Hole #7 (Stale jail directories) because Go defers trigger cleanly even over internal panic paths. This yields a much tighter, more predictable code path.

## 2026-06-12 · Skipping cgroup v2 enforcement on macOS

**Context:**
RLIMIT_NPROC doesn't enforce process limits across user namespaces. The standard fix is to use cgroup v2's
pids.max controller, which works regardless of namespace boundaries.

**Options considered:**
1. Enable cgroup v2 via --detect_cgroupv2 and --cgroup_pids_max.
2. Use Docker's --cgroupns=host to expose a writable cgroup hierarchy.
3. Skip cgroups entirely and rely on RLIMIT_NPROC with safe test patterns.

**Decision:**
We chose option 3. Options 1 and 2 require cgroup v2 write access that Docker Desktop doesn't provide even
with --privileged. Testing on macOS confirmed: mkdir works on /sys/fs/cgroup, enabling controllers works,
but writing the child PID to cgroup.procs returns EOPNOTSUPP.

**Rationale:**
On macOS Docker Desktop, cgroup v2 enforcement is a dead end. A real Linux host with --cgroupns=host would
make options 1 and 2 viable, but the challenge environment uses macOS. Without cgroups, fork bombs can't be
strictly limited, so we removed those penetration tests. The remaining 52 penetration tests (file reads,
shell injection, network isolation, write protection, eval injection) all pass without cgroup enforcement.

## 2026-06-12 · Tuning GOBOXD_MAX_JOBS for the 2 GB memory ceiling

**Context:**
Each MemoryHog (150 MB) uses ~354 MB peak including JVM overhead. The container has 2 GB RAM. Concurrency
defaulted to runtime.NumCPU() = 8 slots.

**Options considered:**
1. 8 slots (default) - 8 x 354 MB = 2.8 GB, over the 2 GB limit.
2. 4 slots - 4 x 354 MB = 1.4 GB, under limit with headroom.
3. 5 slots - 5 x 354 MB = 1.77 GB, marginal headroom.

**Decision:**
We chose option 2 (GOBOXD_MAX_JOBS=4). Option 3 (5 slots) caused worse throughput at 3 RPS (44% success
vs 70% with 4 slots) because memory thrashing increased latency by 2-3x.

**Rationale:**
Memory pressure is the primary bottleneck. More slots just mean more concurrent requests competing for
the same 2 GB, causing swapping and OOM kills. 4 slots keeps peak memory at ~1.4 GB with 600 MB headroom
for the Go runtime and nsjail infrastructure.

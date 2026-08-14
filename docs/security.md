# Security

## Threat model

goboxd executes untrusted user source code. The attacker controls the source
code content, filenames, compiler flags, test inputs, and resource
allowances. The allowances are within the configured limits.

The goal is to prevent the attacker from:

- Access to files outside the jail directory
- Excessive use of server resources (CPU, memory, disk, processes)
- Escape from the nsjail namespace isolation
- Influence on other concurrent or future executions

## Holes closed

| # | Hole | Fix | Location |
|---|---|---|---|
| 1 | Path traversal via filename | `isValidFilename` rejects `/`, `\`, leading `.`, `..`, max 64 chars | `internal/api/handlers.go` |
| 2 | Shell-style directory commands | Use `os.MkdirTemp` and `os.RemoveAll`, no shell exec. Unit test `TestSecurityHole2NoShellCommands` verifies zero shell invocations project-wide. | `internal/runner/runner.go`, `internal/api/handlers_test.go` |
| 3 | Compiler-flag injection | Per-language allow-list with exact and prefix (`*`) matching | `internal/api/handlers.go` |
| 4 | No request size limits | `http.MaxBytesReader` (256 KiB), test count cap (50), per-field cap (64 KiB), output capped at 64 KiB | `internal/api/handlers.go`, `internal/runner/runner.go` |
| 5 | UID collisions under load | Each jail runs as a distinct unprivileged host uid from a pool. Jail dirs are 0700 and owned by the jail uid. | `internal/uidpool/`, `internal/runner/runner.go` |
| 6 | Unbounded child output | `io.LimitReader` caps stdout/stderr at 64 KiB, `readCapped` adds truncation marker | `internal/runner/runner.go` |
| 7 | Stale jail directories | `defer os.RemoveAll` after every jail dir creation + startup orphan sweep (30 min) | `internal/runner/runner.go`, `cmd/goboxd/main.go` |
| 8 | nsjail error misclassification | `isInfraError` detects pipe and start failures. It separates infrastructure errors from user-code errors in both build and test paths. | `internal/runner/runner.go` |
| 9 | Unbounded concurrency | Channel-based semaphore limits concurrent executions to `runtime.NumCPU()` (or `GOBOXD_MAX_JOBS`), preventing resource exhaustion under burst load | `internal/api/handlers.go` |
| 10 | Server crash on handler panic | `RecoveryMiddleware` catches panics in all handlers, logs stack trace, returns 500. One bad request cannot crash the server. | `internal/api/logging.go` |
| 11 | Sandbox escape via dangerous syscalls | `--seccomp_policy` passes the embedded deny-list policy to every jail (build and run). kafel compiles it at jail start; DENY is SECCOMP_RET_KILL. | `internal/seccomp/seccomp.policy`, `internal/runner/runner.go` |
| 12 | Memory limits not enforced | nsjail's `--rlimit_as` takes MB. The runner passed bytes. Limits were about 1024x too large. The guard is now tight and equal to the memory limit. | `internal/runner/runner.go` |
| 13 | Symlink race on the source write | `writeSource` opens the source path with `O_EXCL` and `O_NOFOLLOW`. A planted symlink fails the open instead of being followed. | `internal/runner/runner.go` |
| 14 | Memory and pids limits without cgroup v2 | Per-jail cgroup v2 dirs enforce `memory.max` and `pids.max`. Peak memory and OOM events come from the cgroup. The rlimit fallback stays active when cgroup v2 is not available. | `internal/cgroupv2/` |

## What each fix does

### Hole 1 — Filename validation
The server checks all source and artifact filenames from the client before
use. Each name must be a single path component. It must not contain `/` or
`\`. It must not start with `.`. It must not contain `..`. The maximum length
is 64 characters. This prevents file writes outside the jail directory.

### Hole 2 — No shell commands
Naive implementations shell out via `os.system()` with string formatting.
goboxd uses the Go filesystem APIs directly. The APIs are `os.MkdirTemp`,
`os.RemoveAll`, and `os.WriteFile`. Every path operation uses
`filepath.Join` to prevent path injection. A project-wide scan in
`TestSecurityHole2NoShellCommands` verifies that the service does not invoke
a shell binary through `exec.Command`.

### Hole 3 — Flag allow-list
Each compiled language has a list of permitted compiler flags. Flags can be
allowed exactly (`-O2`) or by prefix (`-std=*`). The HTTP layer checks the
allow-list before any execution. It returns HTTP 400 for disallowed flags.
This prevents flag injection attacks, for example `-fplugin=evil.so` and
`@payload`.

### Hole 4 — Request limits
- Request body: 256 KiB via `http.MaxBytesReader`
- Test count: max 50
- Per-field stdin/expected_stdout: max 64 KiB each
- Child stdout/stderr: capped at 64 KiB per stream with truncation marker
- File writes inside jail: limited via `--rlimit_fsize 100` (100 MB)

### Hole 5 — One unprivileged uid per jail
The uid pool gives each jail one uid from a fixed range. The range starts
at `GOBOXD_UID_MIN` (default 10000). The pool size equals the concurrency
semaphore bound. The pool can never be empty while the server admits jobs.
An allocation failure returns `internal_error`. Two jails never share a uid.

The jail dir starts root-owned with mode 0700. The runner chowns it to the
jail uid. The jailed process can read and write its own dir. Other jails
cannot traverse it. The nsjail uid map is `U:U:1` plus `0:0:1`. The first
map pins the process to unprivileged host uid U. The second map lets the
nsjail setup phase run. An escape from one jail yields only uid U
privileges. It never yields root.

### Hole 6 — Output capping
The server reads child stdout and stderr through `io.LimitReader`. If output
exceeds 64 KiB, the server truncates it. The server appends
`... [output truncated]` so the caller knows. This prevents unbounded memory
consumption from chatty processes.

### Hole 7 — Cleanup on every path
`defer os.RemoveAll(jailDir)` runs immediately after directory creation. It
ensures cleanup on panic, error, or success. On startup, `SweepOrphans`
removes any leftover jail dirs older than 30 minutes.

### Hole 8 — nsjail error classification
`isInfraError` detects infrastructure failures. A failure is an
infrastructure failure when nsjail cannot create a pipe or cannot start the
process. The check runs in both `runBuild` and `runSingleTest`. These
failures produce `internal_error` status. They are not misclassified as
`build_failed` or `runtime_error`.

Exit codes do not mean infrastructure failure. nsjail propagates the exit
code of the user program. The code 255 comes from the user program, not from
nsjail.

### Hole 9 — Concurrency limiting
A channel-based semaphore (`jobSem`) limits concurrent executions. This
prevents resource exhaustion. The capacity defaults to `runtime.NumCPU()`.
You can override it with the `GOBOXD_MAX_JOBS` environment variable.
Requests queue when the semaphore is full.

### Hole 11 — Sandbox escape via dangerous syscalls
The embedded deny-list policy `internal/seccomp/seccomp.policy` is passed to
every jail via `--seccomp_policy` (build and run steps share `nsjailArgs`).
nsjail compiles it with kafel at jail startup and applies the resulting
seccomp-bpf filter; DENY is SECCOMP_RET_KILL and DEFAULT ALLOW keeps normal
code execution working. A denied syscall (e.g. `mount`, `ptrace`, `bpf`)
kills the jailed process with SIGSYS, which the runner reports as
`runtime_error`.

Two kafel quirks required workarounds. kafel's lexer only accepts `//` and
`/* */` comments (no `#`), and kafel's amd64 syscall table is missing
`umount2`, so the policy references it by number (`SYSCALL[166]`, the x86_64
number shared with umount). nsjail vendors its own maintained kafel fork
(the standalone google/kafel repo is dormant), and the fork's amd64 table
still lacks umount2, so `SYSCALL[166]` remains the durable workaround.

### Hole 12 — Memory limits are now real
nsjail reads `--rlimit_as` in megabytes. The runner passed kilobytes times
1024. Every memory limit was about 1024x too large. The fix converts the
limit to MB and passes it 1:1. RLIMIT_AS caps virtual address space. Some
runtimes reserve large virtual regions at startup. The registry raises the
limits for those runtimes. The registry excludes the two that cannot fit
tight limits (csharp and elixir). See the deployment section.

### Hole 13 — Symlink race on the source write
The source write uses `O_EXCL` and `O_NOFOLLOW`. A symlink at the source
path fails the open. The write never follows the link. The unit test
`TestWriteSourceRejectsSymlink` plants a symlink and asserts the failure.

### Hole 14 — cgroup v2 enforcement
When the host exposes a writable cgroup2 hierarchy, goboxd creates one
cgroup dir per jail. nsjail moves each exec into a leaf under it. The leaf
enforces `memory.max` and `pids.max`. The jail dir reports `memory.peak`
for the response. A polling loop reads `memory.events` to classify cgroup
OOM kills as `memory_exceeded`.

The startup probe proves that the memory controller really charges memory.
It runs a small hog in a probe cgroup. If the peak does not move, the probe
fails. The server then uses the rlimit path. Limits are never unenforced.

## Deployment

The cgroup v2 path needs a writable cgroup2 filesystem. On systemd hosts,
run goboxd as root. Docker Desktop does not provide a writable hierarchy.
It uses the rlimit path. Set `GOBOXD_CGROUPV2=off` to force the rlimit path.
The server reports the state in `/info` under `cgroupv2`.

Environment variables:

- `GOBOXD_UID_MIN`: first uid in the jail uid pool. Default 10000.
- `GOBOXD_CGROUPV2`: `auto` (default), `on`, or `off`.
- `GOBOXD_EXCLUDE_LANGS`: comma list of languages to remove from the
  registry. The image keeps them. The default is empty (all languages run).

Run `scripts/dev-host.sh` for a native host server without Docker. It
advertises the languages installed on the host.

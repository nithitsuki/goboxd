# Security

## Holes closed

| # | Hole | Fix | Location |
|---|---|---|---|
| 1 | Path traversal via filename | `isValidFilename` rejects slashes, leading dots, `..` | `boxd/api/handlers.go:72` |
| 2 | Shell-style directory commands | Use `os.MkdirTemp` and `os.RemoveAll`, no shell exec | `boxd/runner/runner.go:22` |
| 3 | Compiler-flag injection | Per-language allow-list with exact and prefix matching | `boxd/api/handlers.go:88` |
| 4 | No request size limits | `http.MaxBytesReader` (256 KiB), `rlimit_fsize` (100 MB), test count cap (50), output capped at 64 KiB | `boxd/api/handlers.go:330`, `boxd/runner/runner.go:11` |
| 5 | UID collisions under load | `os.MkdirTemp` guarantees unique directory names | `boxd/runner/runner.go:22` |
| 6 | Unbounded child output | `io.LimitReader` caps stdout/stderr at 64 KiB, `readCapped` adds truncation marker | `boxd/runner/runner.go:240` |
| 7 | Stale jail directories | `defer os.RemoveAll` after jail dir creation + startup orphan sweep | `boxd/runner/runner.go:28`, `cmd/goboxd/main.go:12` |

## What each fix does

### Hole 1 — Filename validation
All source and artifact filenames from the client are checked before use. They must be a single path component (no `/` or `\`), no leading `.`, no `..`, and max 64 characters.

### Hole 2 — No shell commands
The reference implementation used `os.system()` with string formatting. goboxd uses Go's filesystem APIs directly. Every path operation uses `filepath.Join` and `os` functions.

### Hole 3 — Flag allow-list
Each language has a list of permitted compiler flags. Flags can be allowed exactly (`-O2`) or by prefix (`-std=*`). The allow-list is checked at the HTTP layer before execution, returning 400 for disallowed flags.

### Hole 4 — Request limits
The request body is limited to 256 KiB via `http.MaxBytesReader`. Test count is capped at 50. Inside the sandbox, `rlimit_fsize` limits file writes to 100 MB. Child process output is capped at 64 KiB per stream.

### Hole 5 — Unique directories
`os.MkdirTemp` creates directories with random suffixes. No collision can occur, unlike the reference's 30k-range UID retry loop.

### Hole 6 — Output capping
Child stdout and stderr are read through `io.LimitReader`. If output exceeds 64 KiB, it is truncated and `... [output truncated]` is appended so the caller knows.

### Hole 7 — Cleanup on every path
`defer os.RemoveAll(jailDir)` runs immediately after directory creation, ensuring cleanup on panic, error, or success. On startup, `SweepOrphans` removes any leftover jail dirs older than 30 minutes.

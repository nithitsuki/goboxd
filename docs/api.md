# API Reference

## POST /run

Executes untrusted code inside an nsjail sandbox. Returns 200 after the run completes regardless of user-code outcome.

### Request

```json
{
  "language": "py3",
  "source": "print(\"hello\")",
  "source_filename": "solution.py",
  "artifact_filename": "",
  "build": {
    "limits": { "wall_time_s": 5, "memory_kb": 1048576, "max_processes": 100 },
    "flags": ["-O2"]
  },
  "run": {
    "limits": { "wall_time_s": 3, "memory_kb": 524288, "max_processes": 64 },
    "flags": []
  },
  "tests": [
    { "stdin": "1\n", "expected_stdout": "hello" }
  ]
}
```

Fields:
- `language` (required) — language id from the registry. returns 400 if unknown.
- `source` (required) — utf-8 source code, max 256 KiB.
- `source_filename`, `artifact_filename` — required by some languages (java). single path component, no traversal.
- `build` (optional) — limits and flags for compilation. missing fields fall back to language defaults.
- `run` (optional) — limits and flags for execution. missing fields fall back to language defaults.
- `tests` (required) — at least one test case. max 50.

### Response

```json
{
  "status": "accepted",
  "build": {
    "status": "ok",
    "stdout": "",
    "stderr": "",
    "duration_ms": 412
  },
  "tests": [
    {
      "status": "accepted",
      "stdout": "hello",
      "stderr": "",
      "duration_ms": 38,
      "memory_peak_kb": 8192
    }
  ]
}
```

Top-level status rules:
- `accepted` — build ok and every test accepted
- `build_failed` — build step failed (all tests get not_executed)
- `internal_error` — server-side failure during build
- otherwise — the first non-accepted test status

### Errors (400)

```json
{ "error": { "code": "invalid_filename", "message": "filename must be a single path component" } }
```

Error codes: `invalid_request`, `missing_language`, `unknown_language`, `missing_source`, `missing_tests`, `too_many_tests`, `invalid_filename`, `invalid_flags`, `internal_error`.

## GET /healthz

Liveness check. Returns `200 {"status":"ok"}`.

## GET /readyz

Readiness probe. Returns 200 if nsjail and all language runtimes pass `--version`. Returns 503 with per-language breakdown on failure.

```json
{
  "status": "degraded",
  "nsjail": { "ok": true, "version": "3.4" },
  "languages": {
    "py3": { "ok": true, "version": "Python 3.11.2" },
    "java": { "ok": false, "error": "javac not found at /usr/bin/javac" }
  }
}
```

## GET /info

Service metadata. Always 200.

```json
{
  "build_info": { "version": "0.1.0", "commit": "abc1234", "go_version": "go1.26.3" },
  "nsjail": { "path": "/usr/bin/nsjail", "version": "3.4" },
  "languages": [
    {
      "id": "py3",
      "name": "Python 3",
      "version": "Python 3.11.2",
      "default_run_limits": { "wall_time_s": 9, "memory_kb": 102400, "max_processes": 100 }
    }
  ],
  "limits": { "max_source_bytes": 262144, "max_tests": 50, "max_concurrent_jobs": 16 },
  "stats": {
    "in_flight_jobs": 0,
    "jobs_total": 42,
    "jobs_failed_internal": 1,
    "last_internal_error_at": "2026-05-28T10:00:00Z",
    "disk_free_bytes_jail_dir": 53687091200
  }
}
```

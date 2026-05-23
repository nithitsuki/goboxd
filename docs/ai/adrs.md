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

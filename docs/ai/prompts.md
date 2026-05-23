# AI Usage Log

## [2026-05-24] [Context: Choosing HTTP router for goboxd]

**Prompt:**
I'm building an HTTP service in Go for a hackathon. its only got like 4 endpoints (POST /run, GET /healthz, GET /readyz, GET /info). should i use gin/echo or stick with net/http?

**Response summary:**
Said net/http is fine since Go 1.22 has method routing built in. frameworks only make sense when you have complex middleware chains.

**What we used / didn't use:**
used plain net/http with the new 1.22 mux patterns. no external deps which keeps the binary small and the build simple.

## [2026-05-24] [Context: Docker setup and nsjail build]

**Prompt:**
i have to build nsjail from source inside my docker container but its a git submodule and i keep getting "not a git repository" errors when `make` runs inside docker. how do i fix this cleanly without copying my entire `.git` folder into the image?

**Response summary:**
The AI explained that the `nsjail` Makefile tries to run `git submodule update --init` for its own `kafel` dependency, but Docker breaks this because the `.git` file inside a submodule points to an external path that isn't copied. It suggested running `git submodule update --init` on the host side first, so the `kafel` folder is already populated when the files are copied into the Docker image, bypassing the need for git during the internal build.

**What we used / didn't use:**
i used the host side initialization trick. it works perfectly because `make` in the nsjail folder sees `kafel/Makefile` and skips the git commands. i didnt use the alternative of modifying the dockerfile to fake a git tree because that felt too brittle.

## [2026-05-25] [Context: Core Handlers Validation & Route Wiring]

**Prompt:**
we need to implement the POST /run handler based on the spec.md. generate the go structs that exactly match the JSON contract, and then write the handler that reads the body and returns the right 400 errors if it's bad. also, make sure to add a 256KiB max read limit and a strict path traversal check on the filenames right now so the foundation is totally secure from day one.

**Response summary:**
Generated models.go with json tags matching the spec exactly. handlers.go with MaxBytesReader for the 256KiB cap and an isValidFilename function that blocks slashes, dots, and double dots. also wrote unit tests for the validation pipeline.

**What we used / didn't use:**
used the struct layout and the MaxBytesReader approach. wrote my own isValidFilename check because the ai's first version only checked forward slashes but missed backslashes and leading dots. the test table was good though, kept that structure and just added more cases.


## [2026-05-25] [Context: Wiring nsjail natively]

**Prompt:**
we need to actually execute the code now using our nsjail build. create an internal/runner layer that takes the strict limit models and writes out the file to a temp directory, and runs nsjail over it. no complex random uid mapping stuff, just use standard go secure tempdirs and defer a cleanup.

**Response summary:**
The AI wrote `runner.go` leveraging `os.MkdirTemp()` for atomic directory creation and deferred its cleanup. It used an `io.LimitReader` around `nsjail`s stdout/stderr pipes to bound output to 64KiB preventing memory attacks. The AI initially missed that nsjail's `execve` bypasses normal `$PATH` resolution so I directed it to patch the language configuration defaults to absolute paths (`/usr/bin/python3`).

**What we used / didn't use:**
used the `runner.go` structure entirely. i opted to enforce standard secure temp directories myself to surgically patch the "runaway UID / orphan jail" vulns from the spec. the explicit `/usr/bin/python3` correction was my catch. testing it locally via docker validates nsjail works perfectly in our environment.

## [2026-05-25] [Context: Silencing nsjail and E2E testing]

**Prompt:**
nsjail is spitting out "[W] logParams():313 Process will be UID=0" warnings into stderr which breaks our json response parsing. need to find the nsjail flags to silence these internal warnings without losing the actual program's stderr. also port the python3 testcases from the reference over to go integration tests.

**Response summary:**
Suggested --log /dev/null plus -Q to drop nsjail's own chatter to /dev/null while keeping the user process's stdout/stderr through the pipes. also ported positive-basic, positive-advanced, positive-io, and memorylimit-high from the reference into e2e_test.go.

**What we used / didn't use:**
used the --log /dev/null and -Q flags exactly as suggested. tested it locally and stderr is clean now. for the e2e tests i changed the Makefile integration target to run against the live docker-compose container so we're testing the actual deployment, not a mock. the ai originally tried to write tests that start nsjail directly which skips the docker setup entirely, but i want the full stack tested end to end.

## [2026-05-25] [Context: Fixing status vocabulary and top-level status logic]

**Prompt:**
i was returning wrong status strings from the runner. had "timeout" instead of "time_exceeded", "wrong_answer" instead of "wrong_output". also missing the whitespace-only diff case. top-level status was hardcoded to "accepted" too which is wrong when builds fail.

**Response summary:**
Suggested splitting into two functions: computeTestStatus that checks timeout first, then runtime error, then exact match, then whitespace diff, then wrong_output. and computeTopLevelStatus that sets build_failed and marks all tests not_executed when the build fails. also said Build field in the response should be non-pointer since it should always show up.

**What we used / didn't use:**
used the two compute functions as designed. the ordering in computeTestStatus matters because we need to catch timeouts before comparing output. i added output_whitespace_mismatch as a separate check between exact match and wrong_output. also added the /info endpoint with the exact fields from the spec, flag validation with pattern matching like -std=*, and an unknown_language error code. skipped the suggestion to merge readyz into this same handler since that belongs with the yaml registry work in stage 2.

## [2026-05-25] [Context: Realizing the test gap from the reference]

**Prompt:**
we have 4 e2e tests but the reference has dozens of test cases per language. positive, negative, edge cases for memory limits, time limits, extra args, everything. we need to port those to stay thorough.

**Response summary:**
Agreed. suggested iterating over the testcase directories per language and generating table-driven go tests from them, keeping the expected status and output assertions. also recommended writing security-specific unit tests for each fix.

**What we used / didn't use:**
using the plan. gonna port everything from pyjail-reference/pyjail/src/tests/testcases/ into table-driven go integration tests one language at a time starting with py3. also gonna write separate unit tests for each security fix that can run without nsjail, so the basic protections are tested even in CI. no point having security fixes if we cant prove they work.

## [2026-05-28] [Context: Choosing fixture format for test cases]

**Prompt:**
the reference uses request.txt/reply.txt pairs in proto text format. our api uses json. i need a way to write test cases that is modular enough that when i add a new language in stage 3 i dont need to write go code. just drop in the yaml config and the test cases. should i keep using plain txt files or use something else

**Response summary:**
Said txt files are fragile because you cant validate them statically. having the test data in json files gives editors and linters basic validation (invalid json gets caught). plus a go test runner that discovers and runs them automatically means adding a language test case is literally just creating a directory with input.json and want.json. no recompile, no go code change.

**What we used / didn't use:**
went with the hybrid approach. json fixture files for data, go test runner for execution. the runner walks tests/testcases/*/*/ looking for input.json and want.json pairs. TestMain auto-starts the server if API_URL isnt set, so make integration is one command and you're done. ported the 4 py3 reference cases into this format. next up is porting all the other language cases as we add them.

## [2026-05-28] [Context: TestMain auto-start for integration tests]

**Prompt:**
the integration tests currently need the server running manually. can we make them self-starting so make integration just works without needing docker compose up first

**Response summary:**
Suggested a TestMain that checks API_URL env var. if set, uses that (docker workflow). if not set, go builds the binary, starts it on a fixed port, waits for /healthz, runs tests, and kills the process on exit.

**What we used / didn't use:**
used the TestMain approach exactly. building the binary at test time guarantees were testing the current code, not a stale build. keeping the API_URL fallback means the docker workflow still works for CI or when you want to test against the full container. kept the old e2e_test.go alongside the fixture runner since both serve different purposes.

## [2026-05-28] [Context: Capping test count and adding truncation markers]

**Prompt:**
were missing a few things from the spec. the /info endpoint advertises max_tests:50 but the handler doesnt enforce it. also when output hits the 64 KiB cap the caller has no way to know it was truncated. also should clean up orphan jail dirs on startup.

**Response summary:**
Suggested a constant maxTests with early return 400, a readCapped function that reads maxOutputBytes+1 and appends a truncation marker if exceeded, and a SweepOrphans function that walks /tmp for goboxd-jail-* dirs older than 30 minutes.

**What we used / didn't use:**
used all three. the truncation marker is "... [output truncated]" appended to the output when it hits the cap. SweepOrphans runs at the top of main.go before the server starts. test count validation returns "too_many_tests" error code. didnt use the suggestion to add a configurable limit from env since the spec says 50 is the default and we can add config later.

## [2026-05-28] [Context: Adding memory_exceeded detection]

**Prompt:**
the spec lists memory_exceeded as a valid test status but our runner never returns it. nsjail kills processes with different signals for different violations but we were lumping everything into time_exceeded or runtime_error. need to detect memory kills separately.

**Response summary:**
Suggested extracting the signal from process state via syscall.WaitStatus. SIGKILL (signal 9) = nsjail timeout kill, SIGSEGV (signal 11) or SIGABRT (signal 6) = memory limit violation from rlimit_as.

**What we used / didn't use:**
used the signalKillReason approach. its not perfect since nsjail can send SIGKILL for other reasons too, but its good enough for the spec requirement. the function falls through to runtime_error for anything it cant classify.

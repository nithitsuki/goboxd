# AI Usage Log

## [2026-05-24] [Context: Choosing HTTP router for goboxd]

**Prompt:**
I'm building an HTTP service in Go 1.23 for a hackathon. The service needs to be extremely fast and robust under load. The API only has about 4 endpoints (`POST /run`, `GET /healthz`, `GET /readyz`, `GET /info`). Should I use a framework like Gin/Echo, or stick to the standard library?

**Response summary:**
The AI suggested that for an API with very few endpoints, the standard library `net/http` is perfectly sufficient, especially since Go 1.22 introduced method and path-based routing (e.g., `mux.HandleFunc("GET /healthz", ...)`). It noted that avoiding third-party frameworks eliminates external dependencies and overhead, staying closer to the metal, which is preferred for high-concurrency robust systems unless complex middleware is needed.

**What we used / didn't use:**
We used the recommendation to stick purely to `net/http` using the new Go 1.22+ routing features. We didn't use any external frameworks because the standard library is absolutely enough for our simple routing needs, minimizing dependency footprint and maximizing raw throughput.

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
The AI generated `internal/api/models.go` with strict JSON tags mapping to the spec, and `internal/api/handlers.go`. It used `http.MaxBytesReader` for the 256KiB limit and wrote a custom `isValidFilename` function to block path traversals (checking for slashes and `..`). It also provided unit tests for validation rules.

**What we used / didn't use:**
used exact structs and handlers as suggested. I requested the max size limit and path validation specifically because security is a top priority for this architecture, and laying down a rock solid secure foundation early prevents massive refactors later. wired it up with clean tests, zero lint errors. foundation is super solid now and we are in a great spot.


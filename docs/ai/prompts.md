# AI Usage Log

## [2026-05-24] [Context: Choosing HTTP router for goboxd]

**Prompt:**
I'm building an HTTP service in Go 1.23 for a hackathon. The service needs to be extremely fast and robust under load. The API only has about 4 endpoints (`POST /run`, `GET /healthz`, `GET /readyz`, `GET /info`). Should I use a framework like Gin/Echo, or stick to the standard library?

**Response summary:**
The AI suggested that for an API with very few endpoints, the standard library `net/http` is perfectly sufficient, especially since Go 1.22 introduced method and path-based routing (e.g., `mux.HandleFunc("GET /healthz", ...)`). It noted that avoiding third-party frameworks eliminates external dependencies and overhead, staying closer to the metal, which is preferred for high-concurrency robust systems unless complex middleware is needed.

**What we used / didn't use:**
We used the recommendation to stick purely to `net/http` using the new Go 1.22+ routing features. We didn't use any external frameworks because the standard library is absolutely enough for our simple routing needs, minimizing dependency footprint and maximizing raw throughput.

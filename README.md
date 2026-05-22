# goboxd

A sandbox daemon for running untrusted code inside nsjail.

I chose the standard net/http package for our framework because go 1.22 added native routing that handles our strict api perfectly. dodging third-party frameworks keeps our binary small and predictable under heavy concurrent load.

Commands:
- `make build` to build the Docker container
- `make run` to start the server locally
- `make test` to run tests
- `make integration` to run integration tests
- `make load` to run load tests
- `make lint` to run static analysis

See the `docs/` folder for architecture details, API specifications, and benchmarks.
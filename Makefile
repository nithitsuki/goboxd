.PHONY: build run test integration integration-docker integration-safe lint fmt vulncheck bench

# Pinned Go toolchain (single source of truth: .go-version). The `make lint`
# target forces GOTOOLCHAIN to this exact version so golangci-lint is
# deterministic even when the host default Go is newer (golangci-lint crashes
# on the Go 1.27 internal API changes). Matches .github/workflows/ci.yml.
GO_VERSION := $(shell cat .go-version 2>/dev/null || echo "1.25.13")
GOTOOLCHAIN := GOTOOLCHAIN=go$(GO_VERSION)

build:
	./scripts/build.sh $(LANGS)

run:
	LANGS="$(LANGS)" docker compose up

test:
	$(GOTOOLCHAIN) go test -count=1 ./internal/... ./cmd/...

integration:
	$(GOTOOLCHAIN) go test -v -count=1 ./tests/integration/...

integration-docker:
	$(GOTOOLCHAIN) API_URL=http://localhost:8080 go test -v -count=1 ./tests/integration/...

integration-safe:
	$(GOTOOLCHAIN) SKIP_PENETRATION=1 API_URL=http://localhost:8080 go test -v -count=1 ./tests/integration/...

bench:
	$(GOTOOLCHAIN) go test -bench=BenchmarkRunThroughput -benchtime=5s -run='^$$' ./tests/integration/...

load:
	./scripts/bench.sh

load-save:
	BENCH_SAVE=1 ./scripts/bench.sh

lint:
	$(GOTOOLCHAIN) golangci-lint run ./... && $(MAKE) vulncheck

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

fmt:
	go fmt ./...

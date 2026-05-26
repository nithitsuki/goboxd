.PHONY: build run test integration integration-docker load lint fmt

build:
	docker compose build

run:
	docker compose up

test:
	go test -v ./internal/...

integration:
	go test -v -count=1 ./tests/integration/...

integration-docker:
	API_URL=http://localhost:8080 go test -v -count=1 ./tests/integration/...

load:
	./scripts/bench.sh

load-save:
	BENCH_SAVE=1 ./scripts/bench.sh

lint:
	golangci-lint run ./...

fmt:
	go fmt ./...

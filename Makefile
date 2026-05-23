.PHONY: build run test integration load lint

build:
	docker compose build

run:
	docker compose up

test:
	go test -v ./...

integration:
	API_URL=http://localhost:8080 go test -v ./tests/integration/...
load:
	# Placeholder for sustained load testing via hey/k6
	echo "load tests pending"

lint:
	golangci-lint run ./...

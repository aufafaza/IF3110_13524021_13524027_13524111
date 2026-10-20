.PHONY: run build test test-integration vet

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test -race ./...

test-integration:
	go test -race -tags=integration ./test/integration/...

vet:
	go vet ./...

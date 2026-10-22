TEST_DATABASE_URL ?= postgres://test:test@localhost:5433/platform_test?sslmode=disable

.PHONY: run build test test-integration test-db-up test-db-down vet

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test -race ./...

test-db-up:
	docker compose -f docker-compose.test.yml up -d --wait

test-db-down:
	docker compose -f docker-compose.test.yml down -v

test-integration: test-db-up
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -p 1 -race -tags=integration ./...

vet:
	go vet ./...

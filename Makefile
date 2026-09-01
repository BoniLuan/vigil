.PHONY: build fmt generate lint test test-integration test-race db-up db-down migrate

VERSION ?= dev
COMMIT ?= none
BUILD_DATE ?= unknown
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)

build:
	go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/vigil ./cmd/vigil

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

generate:
	docker run --rm -v "$$(pwd):/src" -w /src sqlc/sqlc:1.31.1 generate

lint:
	go vet ./...

test:
	go test ./...

test-integration:
	test -n "$$VIGIL_TEST_DATABASE_URL"
	go test -count=1 ./...

test-race:
	go test -race ./...

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

migrate:
	go run ./cmd/vigil migrate up

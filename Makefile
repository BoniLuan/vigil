.PHONY: build fmt lint test test-race db-up db-down migrate

VERSION ?= dev
COMMIT ?= none
BUILD_DATE ?= unknown
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)

build:
	go build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o bin/vigil ./cmd/vigil

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

lint:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

migrate:
	go run ./cmd/vigil migrate up

BINARY  := waniis
PKG     := github.com/elmkarami/waniis-cli
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(PKG)/internal/commands.Version=$(VERSION) -X $(PKG)/internal/commands.Commit=$(COMMIT) -X $(PKG)/internal/commands.Date=$(DATE)

.PHONY: build test lint vet fmt check completions

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/waniis

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint:
	golangci-lint run ./...

# What CI runs: formatting, vet and the tests.
check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)
	go vet ./...
	go test -race ./...

completions:
	./scripts/completions.sh

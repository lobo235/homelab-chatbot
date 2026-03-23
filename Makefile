export PATH := $(HOME)/bin/go/bin:$(PATH)

BINARY  := homelab-chatbot
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build test cover lint run clean hooks

build:
	CGO_ENABLED=1 go build -trimpath $(LDFLAGS) -o $(BINARY) ./cmd/server

test:
	CGO_ENABLED=1 go test ./...

cover:
	CGO_ENABLED=1 go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

lint:
	golangci-lint run ./...

run:
	CGO_ENABLED=1 go run ./cmd/server

hooks:
	git config core.hooksPath .githooks

clean:
	rm -f $(BINARY) coverage.out

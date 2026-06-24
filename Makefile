GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOFUMPT ?= gofumpt

BINARY ?= tronvent
BUILD_DIR ?= bin
MAIN ?= ./main.go
LDFLAGS ?= -s -w

.PHONY: help build test lint format fmt fmt-check vet tidy clean docker

help:
	@echo "Targets: build test lint format fmt-check vet tidy clean docker"

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(MAIN)

test:
	$(GO) test ./...

lint:
	$(GOLANGCI_LINT) run ./...

format fmt:
	$(GO) fmt ./...
	@command -v $(GOFUMPT) >/dev/null 2>&1 && $(GOFUMPT) -w . || true

fmt-check:
	@test -z "$$($(GO) fmt ./...)" || (echo "go fmt changed files — run 'make format'"; exit 1)
	@command -v $(GOFUMPT) >/dev/null 2>&1 && test -z "$$($(GOFUMPT) -l .)" || (echo "gofumpt needed — run 'make format'"; $(GOFUMPT) -l .; exit 1)

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BUILD_DIR)

docker:
	docker build -t tronvent:local .

check: fmt vet lint test build

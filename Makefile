BINARY  := spec
VERSION ?= dev
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.DEFAULT_GOAL := help

build: ## Build the binary for the current platform
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/spec

fmt: ## Format Go source files
	gofmt -w .

vet: ## Run Go's built-in static analyzer
	go vet ./...

lint: ## Check Go formatting and run go vet
	./scripts/lint.sh

test: ## Run tests
	go test ./...

verify: ## Run lint checks and tests
	./scripts/verify.sh

dist: ### Build binaries for all target platforms
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		out=dist/$(BINARY)-$$os-$$arch; \
		if [ $$os = windows ]; then out=$$out.exe; fi; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $$out ./cmd/spec || exit 1; \
	done

release: ## Create a release and upload binaries to GitHub
	./scripts/release.sh

clean: ## Remove build artifacts
	rm -rf bin dist

help: ## Show this help message
	@echo "Available commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

.PHONY: build fmt vet lint test verify dist release clean help

BINARY  := spec
VERSION ?= dev
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/spec

test:
	go test ./...

dist:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		out=dist/$(BINARY)-$$os-$$arch; \
		if [ $$os = windows ]; then out=$$out.exe; fi; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $$out ./cmd/spec || exit 1; \
	done

release:
	./scripts/release.sh

clean:
	rm -rf bin dist

.PHONY: build test dist release clean

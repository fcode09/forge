# FORGE developer tasks. Nothing here installs tools or touches the network.
BIN     := bin/forge
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/fcode09/forge/internal/version.Version=$(VERSION)

.PHONY: build test vet fmt-check check clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/forge

test:
	go test ./...

vet:
	go vet ./...

# Fails if any file needs gofmt; it never rewrites files.
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: fmt-check vet test

clean:
	rm -rf bin dist

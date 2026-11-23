# Ferry v2 (Go). The legacy Python/Electron tooling keeps its own scripts
# until Phase 5 retires it.

GO        ?= go
# ./... would also match a Go package vendored under desktop/node_modules.
PKG       := ./cmd/... ./internal/...
BIN       := bin/ferry
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
TARGETS   := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64

.PHONY: all build test race lint vet cross clean

all: vet lint race build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/ferry

test:
	$(GO) test $(PKG)

race:
	$(GO) test -race -count=1 $(PKG)

vet:
	$(GO) vet $(PKG)

lint:
	golangci-lint run

# Static cross-builds for every release target (no C toolchain).
cross:
	@set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		echo "build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
			-o bin/ferry-$$os-$$arch ./cmd/ferry; \
	done

clean:
	rm -rf bin

BINARY  = synckeeper
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)
GOFLAGS = -trimpath -ldflags "$(LDFLAGS)"

# Build policy (spec §10): native per platform, cgo permitted. As of W3.2 the
# macOS build uses cgo for the FSEvents watcher backend, so `build`, `test`, and
# `vet` run with cgo at its platform default (on — FSEvents on macOS). The
# pure-Go fsnotify backend stays the universal fallback: it is what compiles
# whenever cgo is off, so cross-compilation (build-all) still works everywhere.

.PHONY: build build-all test vet clean

# build is the supported target: native binary for the host platform (cgo on →
# FSEvents on macOS).
build:
	go build $(GOFLAGS) -o dist/$(BINARY) ./cmd/synckeeper

# build-all is legacy: pure-Go cross-compilation (CGO_ENABLED=0) for a quick
# sanity build of every OS. The FSEvents backend is excluded by its cgo build
# constraint, so these binaries use the fsnotify fallback — the supported macOS
# build with FSEvents is `make build`, run natively on a Mac (spec §10).
build-all:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-linux-amd64 ./cmd/synckeeper
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-arm64 ./cmd/synckeeper
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-amd64 ./cmd/synckeeper
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-windows-amd64.exe ./cmd/synckeeper

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf dist

BINARY  = synckeeper
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)
GOFLAGS = -trimpath -ldflags "$(LDFLAGS)"

# Pure-Go for now, so builds are hermetic. Build policy (spec §10) is native
# per platform; when cgo lands (FSEvents, W3) this drops and `build` becomes the
# only supported target.
export CGO_ENABLED = 0

.PHONY: build build-all test vet clean

# build is the supported target: native binary for the host platform.
build:
	go build $(GOFLAGS) -o dist/$(BINARY) ./cmd/synckeeper

# build-all is legacy: it cross-compiles only while the tree stays pure Go.
# Once cgo (FSEvents) lands the OS-native paths must be built natively (spec §10).
build-all:
	GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-linux-amd64 ./cmd/synckeeper
	GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-arm64 ./cmd/synckeeper
	GOOS=darwin  GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-darwin-amd64 ./cmd/synckeeper
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o dist/$(BINARY)-windows-amd64.exe ./cmd/synckeeper

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf dist

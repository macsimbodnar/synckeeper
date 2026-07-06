BINARY  = synckeeper
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)
GOFLAGS = -trimpath -ldflags "$(LDFLAGS)"

export CGO_ENABLED = 0

.PHONY: build build-all test vet clean

build:
	go build $(GOFLAGS) -o dist/$(BINARY) ./cmd/synckeeper

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

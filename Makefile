.PHONY: build install clean test run deps fmt lint release-local

# Binary name
BINARY=lw
BINARY_PATH=./cmd/lw

# Version from git tags (falls back to "dev")
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# ldflags for version injection
LDFLAGS=-s -w \
	-X github.com/lightwave-media/lightwave-cli/internal/version.Version=$(VERSION) \
	-X github.com/lightwave-media/lightwave-cli/internal/version.Commit=$(COMMIT) \
	-X github.com/lightwave-media/lightwave-cli/internal/version.Date=$(DATE)

# Build the binary
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(BINARY_PATH)

# Install to GOPATH/bin
install:
	go install -ldflags "$(LDFLAGS)" $(BINARY_PATH)

# Install to /usr/local/bin
install-global: build
	sudo cp $(BINARY) /usr/local/bin/$(BINARY)

# Clean build artifacts
clean:
	rm -f $(BINARY) $(BINARY)-*
	rm -rf dist/
	go clean

# Run tests
test:
	go test -v ./...

# Download dependencies
deps:
	go mod download
	go mod tidy

# Run the CLI (for testing)
run:
	go run $(BINARY_PATH) $(ARGS)

# Run task list
task-list:
	go run $(BINARY_PATH) task list --status=approved,next_up

# Run config show
config:
	go run $(BINARY_PATH) config show

# Format code
fmt:
	go fmt ./...

# Lint (requires golangci-lint)
lint:
	golangci-lint run

# Test GoReleaser locally (snapshot, no publish)
release-local:
	goreleaser release --snapshot --clean

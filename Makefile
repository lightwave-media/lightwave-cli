.PHONY: build install clean test run deps fmt lint release-local tools test-ci

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

# Run tests. -race + -shuffle=on are deliberate: PR #57 had a Linux-only
# CI failure (os.Stdout swap race in internal/testutil) that the bare
# `go test` invocation hid on macOS. The race detector catches it
# deterministically on any OS; -shuffle=on protects against future
# order-dependent assumptions of the same shape.
test:
	go test -race -shuffle=on -v ./...

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

# Install all tool versions pinned in .mise.toml.
# Single source of truth for Go + golangci-lint + goreleaser +
# go-junit-report. CI uses the same pins via jdx/mise-action@v4.
tools:
	mise install

# Run tests with JUnit XML output (mirrors CI's Tests job locally).
# Useful before pushing: same exit code semantics as CI. Flags match
# the CI invocation in .github/workflows/base-test.yml; see the `test`
# target for the -race / -shuffle rationale.
test-ci:
	set -o pipefail; \
	go test -race -shuffle=on -v ./... 2>&1 | tee test-output.txt | mise exec -- go-junit-report -set-exit-code > test-results.xml

.PHONY: build install clean test run deps

# Binary name
BINARY=lw
BINARY_PATH=./cmd/lw
VERSION?=2.1.0

# Build the binary
build:
	go build -o $(BINARY) $(BINARY_PATH)

# Install to GOPATH/bin
install:
	go install $(BINARY_PATH)

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

# Build for multiple platforms (without native - for Linux)
build-cross:
	GOOS=darwin GOARCH=amd64 go build -o $(BINARY)-darwin-amd64 $(BINARY_PATH)
	GOOS=darwin GOARCH=arm64 go build -o $(BINARY)-darwin-arm64 $(BINARY_PATH)
	GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux-amd64 $(BINARY_PATH)
	GOOS=linux GOARCH=arm64 go build -o $(BINARY)-linux-arm64 $(BINARY_PATH)

# Create release tarballs
release: build build-cross
	@mkdir -p dist
	tar -czf dist/lw_$(VERSION)_linux_amd64.tar.gz $(BINARY)-linux-amd64
	tar -czf dist/lw_$(VERSION)_linux_arm64.tar.gz $(BINARY)-linux-arm64
	@echo "Release tarballs created in dist/"
	@shasum -a 256 dist/*.tar.gz

# PCB Tracer Makefile

BINARY_NAME=pcb-tracer
BUILD_DIR=build
GO=go

# Version info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
VERSION_PKG=pcb-tracer/internal/version
LDFLAGS=-ldflags "-X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).BuildTime=$(BUILD_TIME) -X $(VERSION_PKG).GitCommit=$(GIT_COMMIT)"

# Default target
.PHONY: all
all: build

# Build the application
.PHONY: build
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) .

# Build for all platforms
.PHONY: build-all
build-all: build-linux build-windows build-darwin

.PHONY: build-linux
build-linux:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 .

.PHONY: build-windows
build-windows:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe .

.PHONY: build-darwin
build-darwin:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 .

# Run the application
.PHONY: run
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

# Install dependencies
.PHONY: deps
deps:
	$(GO) mod download
	$(GO) mod tidy

# Run tests
.PHONY: test
test:
	$(GO) test -v ./...

# Run tests with coverage
.PHONY: coverage
coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Format code
.PHONY: fmt
fmt:
	$(GO) fmt ./...

# Lint code
.PHONY: lint
lint:
	golangci-lint run

# Clean build artifacts
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Install system dependencies (Linux)
.PHONY: install-deps-linux
install-deps-linux:
	@echo "Installing system dependencies for Linux..."
	sudo apt-get update
	sudo apt-get install -y libgl1-mesa-dev xorg-dev
	sudo apt-get install -y tesseract-ocr tesseract-ocr-eng
	sudo apt-get install -y libopencv-dev

# Install system dependencies (macOS)
.PHONY: install-deps-macos
install-deps-macos:
	@echo "Installing system dependencies for macOS..."
	brew install opencv tesseract

# Generate mocks for testing
.PHONY: mocks
mocks:
	mockgen -source=internal/board/spec.go -destination=internal/board/mock_spec.go -package=board

# Package for distribution
.PHONY: package
package: build-all
	@echo "Creating distribution packages..."
	@mkdir -p dist
	tar -czvf dist/$(BINARY_NAME)-$(VERSION)-linux-amd64.tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-linux-amd64
	zip -j dist/$(BINARY_NAME)-$(VERSION)-windows-amd64.zip $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe
	tar -czvf dist/$(BINARY_NAME)-$(VERSION)-darwin-amd64.tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-darwin-amd64
	tar -czvf dist/$(BINARY_NAME)-$(VERSION)-darwin-arm64.tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-darwin-arm64

# Help
.PHONY: help
help:
	@echo "PCB Tracer Build System"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build           Build the application"
	@echo "  build-all       Build for all platforms"
	@echo "  run             Build and run the application"
	@echo "  deps            Download and tidy dependencies"
	@echo "  test            Run tests"
	@echo "  coverage        Run tests with coverage report"
	@echo "  fmt             Format code"
	@echo "  lint            Lint code"
	@echo "  clean           Clean build artifacts"
	@echo "  install-deps-linux   Install Linux system dependencies"
	@echo "  install-deps-macos   Install macOS system dependencies"
	@echo "  package         Create distribution packages"
	@echo "  help            Show this help message"

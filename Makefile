# Node Dashboard Makefile

# Variables
BINARY_NAME=node-dashboard
MAIN_PATH=./cmd/node-dashboard
BUILD_DIR=./build

# Default target
.PHONY: all
all: build

# Development build
.PHONY: build
build:
	@echo "Building for development..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Development build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Production build (Linux executable for Docker)
.PHONY: build-prod
build-prod:
	@echo "Building for production (Linux)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-w -s" -o $(BUILD_DIR)/$(BINARY_NAME)-linux $(MAIN_PATH)
	@echo "Production build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux"

# Run the application in development mode
.PHONY: run
run: build
	@echo "Running node-dashboard..."
	$(BUILD_DIR)/$(BINARY_NAME) --port 8080 --node-exporter http://localhost:9100

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	rm -f node-dashboard
	@echo "Clean complete"

# Install dependencies
.PHONY: deps
deps:
	@echo "Installing dependencies..."
	go mod tidy
	go mod download
	@echo "Dependencies installed"

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Code formatted"

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	go test -v ./...
	@echo "Tests complete"

# Build and run
.PHONY: dev
dev: build run

# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build      - Build for development"
	@echo "  build-prod - Build for production (Linux executable)"
	@echo "  run        - Run the application"
	@echo "  dev        - Build and run in development mode"
	@echo "  clean      - Clean build artifacts"
	@echo "  deps       - Install dependencies"
	@echo "  fmt        - Format code"
	@echo "  test       - Run tests"
	@echo "  help       - Show this help message"
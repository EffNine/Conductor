.PHONY: build test lint clean docker-build docker-run run help

# Binary name
BINARY_NAME=conductor
BINARY_PATH=bin/$(BINARY_NAME)

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOGET=$(GOCMD) get

# Docker parameters
DOCKER_IMAGE=effnine/conductor
DOCKER_TAG=latest

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	$(GOBUILD) -o $(BINARY_PATH) -v ./cmd/conductor

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -cover ./...

## test-coverage: Run tests with coverage report
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

## lint: Run linter
lint:
	@echo "Running linter..."
	@golangci-lint run

## lint-fix: Run linter and auto-fix issues
lint-fix:
	@echo "Running linter with auto-fix..."
	@golangci-lint run --fix

## fmt: Format code
fmt:
	@echo "Formatting code..."
	@gofmt -s -w .

## mod-tidy: Tidy go modules
mod-tidy:
	@echo "Tidying modules..."
	$(GOMOD) tidy

## mod-download: Download dependencies
mod-download:
	@echo "Downloading dependencies..."
	$(GOMOD) download

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -f coverage.out coverage.html
	@echo "Clean complete"

## run: Run the application locally
run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BINARY_PATH)

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

## docker-run: Run Docker container
docker-run:
	@echo "Running Docker container..."
	docker run -p 8080:8080 \
		-e CONDUCTOR_API_KEY=$${CONDUCTOR_API_KEY:-test-key} \
		-e OPENAI_API_KEY=$${OPENAI_API_KEY:-} \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

## docker-push: Push Docker image
docker-push:
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

## fly-deploy: One-shot deploy to Fly.io (needs fly auth + CONDUCTOR_API_KEY)
fly-deploy:
	@chmod +x scripts/fly-deploy.sh
	@./scripts/fly-deploy.sh

## install: Install the binary
install: build
	@echo "Installing $(BINARY_NAME)..."
	@cp $(BINARY_PATH) /usr/local/bin/$(BINARY_NAME)
	@echo "Installed to /usr/local/bin/$(BINARY_NAME)"

## deps: Install development dependencies
deps:
	@echo "Installing development dependencies..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "Dependencies installed"

## help: Show this help message
help:
	@echo "Conductor - Makefile Commands"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Targets:"
	@fgrep -h "##" $(MAKEFILE_LIST) | fgrep -v fgrep | sed -e 's/\\$$//' | sed -e 's/##//'

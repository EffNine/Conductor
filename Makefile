.PHONY: build test lint clean docker-build docker-run run gen-key help test-e2e

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

## test-e2e: Run end-to-end smoke tests (boots the real gateway against mock upstreams)
test-e2e:
	@echo "Running E2E smoke tests..."
	CGO_ENABLED=1 $(GOTEST) -tags=integration -v ./deployments/local-integration/

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

## benchmarks: Run all benchmarks
benchmarks:
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem ./benchmarks/

## benchmarks-verbose: Run benchmarks with verbose output
benchmarks-verbose:
	@echo "Running benchmarks (verbose)..."
	@go test -bench=. -benchmem -run=^$ -v ./benchmarks/

## benchmark: Run a specific benchmark
.PHONY: benchmark
benchmark:
	@echo "Running benchmark: $(BENCH)"
	@go test -bench=$(BENCH) -benchmem ./benchmarks/

## benchmark-cpu: Run benchmarks and capture CPU profile
benchmark-cpu:
	@echo "Running benchmarks with CPU profile..."
	@go test -bench=. -benchmem -cpuprofile=cpu.prof ./benchmarks/
	@echo "CPU profile saved to cpu.prof"
	@go tool pprof -top cpu.prof

## benchmark-mem: Run benchmarks and capture memory profile
benchmark-mem:
	@echo "Running benchmarks with memory profile..."
	@go test -bench=. -benchmem -memprofile=mem.prof ./benchmarks/
	@echo "Memory profile saved to mem.prof"
	@go tool pprof -top mem.prof

## load-test: Run load tests
load-test:
	@echo "Running load tests..."
	@LOAD_TEST=1 go test -v -run=^$ -bench=. -benchtime=5s ./loadtest/

## load-test-concurrent: Run load test with specific concurrency
.PHONY: load-test-concurrent
load-test-concurrent:
	@echo "Running load test with concurrency=$(CONCURRENCY) requests=$(REQUESTS)..."
	@LOAD_TEST=1 go test -v -run=^$ -bench=BenchmarkHTTPChatCompletion -benchtime=${REQUESTS}x ./loadtest/ -args -concurrency=$(CONCURRENCY)

## memory-audit: Run memory audit benchmarks
memory-audit:
	@echo "Running memory audit..."
	@go test -bench=BenchmarkMemoryAudit -benchmem ./loadtest/
	@go test -bench=BenchmarkGoroutineLeakDetection -benchmem ./loadtest/

## gen-key: Print a new random gateway API key
gen-key: build
	@./$(BINARY_PATH) gen-key

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

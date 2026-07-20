# Contributing to Novexa Gateway

Thank you for your interest in contributing to Novexa Gateway! This document provides guidelines and information for contributors.

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](https://www.contributor-covenant.org/version/2/1/code_of_conduct/). By participating, you are expected to uphold this code.

## How to Contribute

### Reporting Bugs

1. Check existing issues to avoid duplicates
2. Use the bug report template
3. Include:
   - Clear description of the bug
   - Steps to reproduce
   - Expected vs actual behavior
   - Environment details (OS, Go version, Docker version)
   - Logs (with sensitive data redacted)

### Suggesting Features

1. Check existing issues and discussions
2. Open a new issue with the feature request template
3. Explain the use case and benefits
4. Be open to discussion and alternatives

### Contributing Code

#### Prerequisites

- Go 1.21 or later
- Docker (for testing)
- Git

#### Development Workflow

1. **Fork the repository**

```bash
git clone https://github.com/YOUR_USERNAME/gateway.git
cd gateway
```

2. **Create a feature branch**

```bash
git checkout -b feature/your-feature-name
```

3. **Make your changes**

- Follow Go best practices
- Write tests for new functionality
- Update documentation as needed
- Ensure code passes linting

4. **Test your changes**

```bash
# Run tests
make test

# Run linter
make lint

# Build
make build

# Test with Docker
make docker-build
```

5. **Commit your changes**

```bash
git add .
git commit -m "feat: add your feature description"
```

We follow [Conventional Commits](https://www.conventionalcommits.org/):
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation changes
- `style:` Code style changes (formatting, etc.)
- `refactor:` Code refactoring
- `test:` Adding or updating tests
- `chore:` Maintenance tasks

6. **Push and create a Pull Request**

```bash
git push origin feature/your-feature-name
```

Then open a Pull Request on GitHub.

#### Pull Request Guidelines

- **Keep it small**: One feature per PR
- **Write a clear description**: Explain what and why
- **Reference issues**: Link related issues
- **Update documentation**: If applicable
- **Add tests**: Ensure new code is tested
- **Follow the checklist**: Complete the PR template

### Adding a New Provider

Adding a new provider is straightforward:

1. **Create provider package**

```bash
mkdir -p internal/provider/yourprovider
```

2. **Implement the Provider interface**

```go
// internal/provider/yourprovider/provider.go
package yourprovider

import "github.com/novexa/gateway/internal/provider"

type Provider struct {
    // your fields
}

func NewProvider(config *Config) *Provider {
    // initialization
}

func (p *Provider) Name() string {
    return "yourprovider"
}

func (p *Provider) ChatCompletion(ctx context.Context, req *model.ChatCompletionRequest) (*model.ChatCompletionResponse, error) {
    // implementation
}

// ... implement other interface methods
```

3. **Add translation logic** (if needed)

If the provider doesn't use OpenAI format, implement translation in `translate.go`.

4. **Add streaming support** (if needed)

Implement streaming normalization in `streaming.go`.

5. **Register the provider**

```go
// cmd/gateway/main.go
import "github.com/novexa/gateway/internal/provider/yourprovider"

// In main()
if cfg.Providers.YourProvider.Enabled {
    registry.Register(yourprovider.NewProvider(&cfg.Providers.YourProvider))
}
```

6. **Add configuration**

```go
// internal/config/types.go
type ProvidersConfig struct {
    // ... existing providers
    YourProvider YourProviderConfig `mapstructure:"yourprovider"`
}

type YourProviderConfig struct {
    Enabled    bool          `mapstructure:"enabled"`
    APIKey     string        `mapstructure:"api_key"`
    BaseURL    string        `mapstructure:"base_url"`
    Timeout    time.Duration `mapstructure:"timeout"`
    MaxRetries int           `mapstructure:"max_retries"`
}
```

7. **Update documentation**

- Add to `docs/providers.md`
- Update `README.md` if needed
- Add example configuration

8. **Write tests**

- Unit tests for translation logic
- Integration tests (if possible with mock server)

9. **Submit PR**

Follow the PR guidelines above.

## Development Setup

### Local Development

```bash
# Clone and setup
git clone https://github.com/novexa/gateway.git
cd gateway

# Install dependencies
go mod download

# Run tests
make test

# Run linter
make lint

# Build
make build

# Run locally
export NOVEXA_API_KEY=test-key
export OPENAI_API_KEY=sk-test
./bin/gateway
```

### Testing

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run specific test
go test -v ./internal/provider/openai/...

# Run integration tests
make test-integration
```

### Linting

```bash
# Run linter
make lint

# Auto-fix issues
make lint-fix
```

### Docker Development

```bash
# Build Docker image
make docker-build

# Run with Docker
make docker-run

# Run tests in Docker
make docker-test
```

## Code Style

### Go Code

- Follow [Effective Go](https://go.dev/doc/effective-go)
- Follow [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` for formatting
- Use `golangci-lint` for linting
- Write meaningful comments
- Use descriptive variable names

### Error Handling

- Always check errors
- Use wrapped errors with context: `fmt.Errorf("failed to do X: %w", err)`
- Return early on errors
- Use custom error types when appropriate

### Testing

- Write unit tests for all new functionality
- Use table-driven tests
- Use meaningful test names
- Mock external dependencies
- Aim for >80% coverage

### Documentation

- Document all exported functions and types
- Include examples in doc comments
- Keep README up to date
- Update CHANGELOG.md

## Project Structure

```
novexa-gateway/
├── cmd/gateway/          # Entry point
├── internal/             # Private application code
│   ├── config/          # Configuration
│   ├── auth/            # Authentication
│   ├── provider/        # Provider adapters
│   ├── router/          # Model routing
│   ├── handler/         # HTTP handlers
│   ├── middleware/      # Fiber middleware
│   ├── model/           # Data types
│   ├── usage/           # Usage tracking
│   ├── health/          # Provider health monitor + per-model reachability probes
│   └── database/        # Database layer
├── pkg/                 # Public reusable packages
├── docs/                # Documentation
├── deployments/         # Deployment configs
└── scripts/             # Utility scripts
```

## Architecture Decisions

See [docs/architecture.md](architecture.md) for detailed architecture documentation.

Key principles:
- **Single responsibility**: Each package has one clear purpose
- **Dependency injection**: Dependencies are injected, not hardcoded
- **Interface-based**: Use interfaces for testability
- **Configuration-driven**: No hardcoded values
- **Provider isolation**: Provider-specific code stays in provider packages

## Getting Help

- Check existing documentation
- Search existing issues and discussions
- Ask in GitHub Discussions
- Join our community chat (if available)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.

## Recognition

Contributors will be recognized in:
- README.md contributors section
- Release notes
- Project documentation

Thank you for contributing to Novexa Gateway! 🚀

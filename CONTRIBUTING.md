# Contributing to TransferGW

Thank you for your interest in contributing! This document provides guidelines and instructions for contributing to the TransferGW project.

## Code of Conduct

All contributors are expected to follow professional and respectful behavior in all interactions.

## Getting Started

### Prerequisites

- Go 1.21 or later
- Kubernetes 1.26 or later
- kubectl configured with cluster access
- Docker (for building images)

### Development Setup

```bash
# Clone the repository
git clone https://github.com/example/transfergw.git
cd transfergw

# Install dependencies
go mod download

# Run tests
make test

# Build operator binary
make build

# Build Docker image
make docker-build
```

## Development Workflow

### 1. Create a Feature Branch

```bash
git checkout -b feature/your-feature-name
```

### 2. Make Changes

Follow these guidelines:
- Use clear, descriptive commit messages
- Keep commits atomic and focused
- Add tests for new functionality
- Update documentation as needed

### 3. Run Tests and Linting

```bash
# Run all checks
make verify

# Run specific checks
make test          # Unit tests
make lint          # Linting
make fmt           # Format code
make vet           # Go vet
```

### 4. Format Your Code

```bash
make fmt
```

### 5. Push and Create Pull Request

```bash
git push origin feature/your-feature-name
```

## Code Style

- Follow Go conventions and idioms
- Use `camelCase` for variables and functions
- Use `PascalCase` for types and exported names
- Add comments for exported types and functions
- Keep functions focused and concise

## Project Structure

```
transfergw/
├── api/v1beta1/              # CRD and types
├── cmd/                       # Entry point
├── controllers/               # Controller logic
├── conversion/                # Conversion engine
├── config/                    # Kustomize configurations
├── examples/                  # Example manifests
├── hack/                      # Build scripts
├── tests/                     # Test files
├── build/                     # Dockerfile
├── docs/                      # Documentation
├── Makefile                   # Build automation
├── go.mod, go.sum            # Dependencies
└── .github/workflows/         # CI/CD pipelines
```

## Commit Message Format

Use conventional commits format:

```
type(scope): subject

body

footer
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

Example:
```
feat(conversion): add support for nginx rewrite annotations

Add translation of nginx.ingress.kubernetes.io/rewrite-target to
gateway.example.com/rewrite policy.

Fixes #123
```

## Pull Request Process

1. Update documentation if changing behavior
2. Add tests for new functionality
3. Ensure all checks pass: `make verify`
4. Provide clear description of changes
5. Link relevant issues
6. Respond to review feedback promptly

## Testing

### Unit Tests

```bash
go test ./... -v
```

### Integration Tests

```bash
go test ./controllers -v -tags=integration
```

### End-to-End Tests

```bash
make kind-create
make kind-load
make test-e2e
make kind-delete
```

## Documentation

- Update IDEA.md for API/design changes
- Update README.md for user-facing changes
- Update SETUP.md for installation/configuration changes
- Add code comments for non-obvious logic
- Include examples for new features

## Reporting Issues

When reporting issues, include:
- Clear description of the problem
- Steps to reproduce
- Expected behavior
- Actual behavior
- Environment details (k8s version, operator version)
- Relevant logs or error messages

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.

## Questions?

- Open an issue for questions
- Check existing issues and documentation first
- Reach out to maintainers if stuck

Thank you for contributing to TransferGW!

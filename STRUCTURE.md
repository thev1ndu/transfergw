# TransferGW Project Structure

Complete directory layout and file organization for the TransferGW Kubernetes operator.

## Directory Tree

```
transfergw/
├── .github/
│   └── workflows/                    # CI/CD automation
│       ├── build.yaml               # Docker build and push
│       └── tests.yaml               # Unit and integration tests
│
├── api/
│   └── v1beta1/                      # CRD API types
│       ├── groupversion_info.go     # API group/version info
│       ├── transfergw_types.go      # TransferGW resource definition
│       └── transfergw_webhook.go    # Validation and default webhooks (TODO)
│
├── cmd/
│   └── main.go                       # Operator entry point
│
├── controllers/
│   ├── transfergw_controller.go     # Main reconciliation logic
│   ├── analyzer.go                  # Ingress analysis (TODO)
│   ├── converter.go                 # Conversion logic (TODO)
│   ├── traffic_manager.go           # Traffic split management (TODO)
│   ├── health_monitor.go            # Metrics and rollback (TODO)
│   └── suite_test.go                # Controller tests
│
├── conversion/
│   ├── engine.go                    # Core conversion engine
│   ├── translators.go               # Annotation translators (TODO)
│   └── engine_test.go               # Conversion tests
│
├── config/
│   ├── kustomization.yaml           # Kustomize root
│   ├── crd/
│   │   ├── bases/
│   │   │   └── gateway.example.com_transfergws.yaml
│   │   └── kustomization.yaml
│   ├── rbac/
│   │   ├── role.yaml                # ClusterRole
│   │   ├── role_binding.yaml        # ClusterRoleBinding
│   │   ├── service_account.yaml     # ServiceAccount
│   │   └── kustomization.yaml
│   ├── manager/
│   │   ├── manager.yaml             # Deployment
│   │   ├── service.yaml             # Services (metrics, webhook)
│   │   ├── webhook.yaml             # Webhook configs
│   │   ├── pdb.yaml                 # PodDisruptionBudget
│   │   └── kustomization.yaml
│   └── samples/
│       ├── simple_migration.yaml    # Simple example
│       └── complex_migration.yaml   # Complex example
│
├── examples/
│   ├── example-simple.yaml          # 25% canary migration
│   └── example-complex.yaml         # Enterprise migration with annotations
│
├── hack/
│   ├── boilerplate.go.txt           # File header template
│   ├── crd-generator.sh             # CRD generation script
│   ├── install.sh                   # Installation script
│   └── uninstall.sh                 # Uninstall script
│
├── docs/
│   ├── IDEA.md                      # Architecture overview
│   ├── README.md                    # Project documentation
│   ├── SETUP.md                     # Installation guide
│   └── PROJECT_SUMMARY.md           # Implementation summary
│
├── tests/
│   ├── e2e/                         # End-to-end tests
│   │   └── suite_test.go
│   └── unit/
│       └── conversion_test.go
│
├── build/
│   └── Dockerfile                   # Multi-stage container build
│
├── Makefile                         # Build automation
├── go.mod                           # Go module definition
├── go.sum                           # Go dependency checksums
├── .gitignore                       # Git ignore rules
├── CONTRIBUTING.md                  # Contribution guidelines
├── LICENSE                          # Apache 2.0 license
├── .gitattributes                   # Git attributes
└── README.md                        # Root README (quick reference)
```

## File Organization

### API Types (`api/v1beta1/`)

- **groupversion_info.go** - API group metadata and scheme registration
- **transfergw_types.go** - TransferGW resource definition with spec/status
- **transfergw_webhook.go** - Validating and mutating webhooks (TODO)

### Controller (`controllers/`)

- **transfergw_controller.go** - Main reconciliation loop
- **analyzer.go** - Ingress analysis and validation (TODO)
- **converter.go** - Conversion orchestration (TODO)
- **traffic_manager.go** - Traffic percentage management (TODO)
- **health_monitor.go** - Metrics collection and rollback (TODO)
- **suite_test.go** - Integration test setup

### Conversion Engine (`conversion/`)

- **engine.go** - Core conversion logic with translator interface
- **translators.go** - Built-in annotation translators (TODO)
- **engine_test.go** - Conversion unit tests

### Configuration (`config/`)

Kustomize-based configuration with overlays:
- **crd/** - CRD resource definitions
- **rbac/** - RBAC resources (ServiceAccount, Roles, Bindings)
- **manager/** - Operator deployment and supporting resources

### Documentation (`docs/`)

- **IDEA.md** - Architecture and design (SIG-Network compliant)
- **README.md** - Comprehensive project documentation
- **SETUP.md** - Installation and quickstart guide
- **PROJECT_SUMMARY.md** - Deliverables breakdown

### Examples (`examples/`)

- **example-simple.yaml** - Simple 25% canary rollout
- **example-complex.yaml** - Complex enterprise migration with Slack alerts

### Build & Automation

- **build/Dockerfile** - Multi-stage container image build
- **Makefile** - Build targets (build, test, deploy, docker, lint)
- **go.mod/go.sum** - Go module dependencies
- **.github/workflows/** - CI/CD pipelines (GitHub Actions)

### Utilities (`hack/`)

- **boilerplate.go.txt** - Standard file header
- **crc-generator.sh** - Generate CRD from types (TODO)
- **install.sh** - Installation helper script (TODO)
- **uninstall.sh** - Uninstall helper script (TODO)

## Key Files

### Critical Files

| File | Purpose | Format |
|------|---------|--------|
| `api/v1beta1/transfergw_types.go` | Resource definition | Go |
| `controllers/transfergw_controller.go` | Main reconciliation logic | Go |
| `conversion/engine.go` | Conversion logic | Go |
| `config/crd/bases/gateway.example.com_transfergws.yaml` | CRD manifest | YAML |
| `config/rbac/role.yaml` | RBAC permissions | YAML |
| `config/manager/manager.yaml` | Operator deployment | YAML |
| `Makefile` | Build automation | Makefile |
| `go.mod` | Dependencies | Go Module |

### Documentation Files

| File | Audience | Focus |
|------|----------|-------|
| `docs/IDEA.md` | Architects | API design, SIG compliance |
| `docs/README.md` | Users | Features, getting started |
| `docs/SETUP.md` | Operators | Installation, configuration |
| `CONTRIBUTING.md` | Contributors | Development workflow |

## Build Artifacts

When built, the following artifacts are generated:

```
bin/
├── manager                          # Operator binary
└── envtest/                         # Test dependencies

dist/
└── transfergw-controller:latest     # Docker image

cover.out                           # Test coverage report
```

## Dependencies

Key external dependencies (see `go.mod`):

- `k8s.io/api` v0.28.0 - Kubernetes API types
- `k8s.io/apimachinery` v0.28.0 - Kubernetes machinery
- `sigs.k8s.io/controller-runtime` v0.16.0 - Controller framework
- `sigs.k8s.io/gateway-api` v1.0.0 - Gateway API types

## Code Organization Principles

1. **Separation of Concerns**
   - `api/` for types only
   - `controllers/` for reconciliation logic
   - `conversion/` for business logic

2. **Testability**
   - Interfaces for mocking
   - Tests co-located with code
   - Integration tests in `tests/`

3. **Configuration Management**
   - Kustomize for overlays
   - ConfigMaps for runtime config
   - Environment variables for operator flags

4. **Documentation**
   - Code comments for non-obvious logic
   - API field comments for CRD documentation
   - SETUP.md for operational guidance

## Extension Points

### Custom Translators

Add annotation translators in `conversion/translators.go`:

```go
type CustomTranslator struct{}

func (t *CustomTranslator) Translate(key, value string) (string, map[string]interface{}, error) {
    // Implementation
}

engine.RegisterTranslator("custom.annotation/key", &CustomTranslator{})
```

### Custom Metrics

Add metrics collection in `controllers/health_monitor.go`:

```go
// Implement MetricsCollector interface
type CustomMetricsCollector struct{}
```

### Gateway-Specific Logic

Add provider-specific logic in `controllers/converter.go`:

```go
// Switch on gatewayClass
switch spec.Conversion.GatewayClass {
case "custom-gateway":
    // Custom logic
}
```

## Development Workflow

1. **Make changes** in appropriate package
2. **Add tests** in `_test.go` file
3. **Run tests** - `make test`
4. **Generate code** - `make generate`
5. **Format code** - `make fmt`
6. **Build binary** - `make build`
7. **Build image** - `make docker-build`
8. **Deploy** - `make deploy`

## Continuous Integration

GitHub Actions workflows (`.github/workflows/`):

- **build.yaml** - Build Docker image and push on tag/merge
- **tests.yaml** - Run tests on PR/push

Makefile targets:

- `make test` - Unit tests
- `make lint` - Linting with golangci-lint
- `make fmt` - Format code
- `make vet` - Go vet
- `make verify` - All checks

## Quick Reference

Common operations:

```bash
# Install dependencies
go mod download

# Run tests
make test

# Format code
make fmt

# Build operator
make build

# Build Docker image
make docker-build

# Deploy to cluster
make deploy

# View logs
kubectl logs -n gateway-system deployment/transfergw-controller

# Create migration
kubectl apply -f examples/example-simple.yaml

# Monitor progress
watch kubectl get transfergws -A
```

## Maintenance

### Adding New Features

1. Update `api/v1beta1/transfergw_types.go` with new fields
2. Add controller logic in `controllers/`
3. Update tests
4. Update documentation
5. Run `make generate` to update CRD

### Breaking Changes

1. Update API version (v1beta2)
2. Add conversion webhook in `transfergw_webhook.go`
3. Update documentation
4. Create migration guide

### Dependencies

Upgrade with:

```bash
go get -u ./...
go mod tidy
make test
```

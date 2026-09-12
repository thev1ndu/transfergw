# TransferGW: Gateway Migration Framework

Production-ready Kubernetes operator for migrating Ingress resources to Gateway APIs with automatic conversion, canary deployments, and health-based rollback.

## Project Structure

```
transfergw/
├── IDEA.md                          # Architecture and design overview
├── README.md                         # This file
├── SETUP.md                          # Installation and quickstart guide
├── crd.yaml                          # TransferGW CRD definition (v1beta1)
├── rbac.yaml                         # ServiceAccount, Roles, RoleBindings
├── operator-deployment.yaml          # Operator controller deployment
├── Dockerfile                        # Multi-stage build for operator image
├── example-simple.yaml               # Simple 25% canary migration example
├── example-complex.yaml              # Complex multi-team with annotations example
│
├── api/
│   └── v1beta1/
│       ├── transfergw_types.go       # TransferGW resource definition
│       ├── transfergw_webhook.go     # Validation and default webhooks
│       └── groupversion_info.go      # API group and version info
│
├── controllers/
│   ├── transfergw_controller.go      # Main reconciliation logic
│   ├── analyzer.go                   # Ingress analysis and validation
│   ├── converter.go                  # Calls conversion engine
│   ├── traffic_manager.go            # Manages percentage routing
│   ├── health_monitor.go             # Metrics comparison and rollback
│   └── suite_test.go                 # Controller tests
│
├── conversion/
│   ├── engine.go                     # Core conversion logic
│   ├── translators.go                # Annotation translators
│   └── engine_test.go                # Conversion tests
│
├── config/
│   ├── manager/
│   │   ├── manager.yaml
│   │   └── controller_manager_config.yaml
│   ├── rbac/
│   │   ├── role.yaml
│   │   ├── role_binding.yaml
│   │   └── service_account.yaml
│   ├── crd/
│   │   ├── bases/
│   │   │   └── gateway.example.com_transfergws.yaml
│   │   └── kustomization.yaml
│   └── samples/
│       ├── simple.yaml
│       └── complex.yaml
│
├── hack/
│   ├── boilerplate.go.txt            # License header
│   └── crd-generator.sh              # Generate CRD from Go types
│
├── go.mod                            # Go module definition
├── go.sum                            # Go dependencies
├── Makefile                          # Build, test, deploy targets
└── .github/
    └── workflows/
        ├── build.yaml                # Docker build CI
        ├── tests.yaml                # Unit/integration tests CI
        └── release.yaml              # Release pipeline

```

## Core Concepts

### TransferGW Resource

Single CRD that manages entire migration lifecycle:

```yaml
apiVersion: gateway.example.com/v1beta1
kind: TransferGW
metadata:
  name: prod-migration
  namespace: transfergw
spec:
  selector:              # Which ingresses to migrate
  conversion:            # How to convert (gateway class, annotation mapping)
  rollout:              # Migration strategy (canary, gradual, immediate)
  monitoring:           # Health checks and auto-rollback thresholds
  lifecycle:            # Cleanup, validation, hooks
status:
  phase:                # Pending → Analyzing → Converting → Canary → Complete
  completionPercentage: # Traffic percentage on gateway
  trafficRouting:       # Current split (ingress %, gateway %)
  metrics:              # Real-time comparison (latency, errors, throughput)
  conditions:           # Standard k8s conditions
```

### Conversion Process

1. **Analyze** operator scans matching ingress resources, detects patterns, validates compatibility
2. **Convert** generates HTTPRoute, Gateway, TLSPolicy resources; translates annotations
3. **Deploy** creates gateway resources and configures initial traffic split
4. **Monitor** continuously compares metrics; auto-rollback on threshold breach
5. **Complete** traffic reaches 100%; marks migration as successful

### Traffic Management

Gradual rollout with configurable strategies:
- **Canary** (default): Route 25% initially, increment by 25% every hour
- **Gradual**: Linear increase over 24-48 hours
- **Immediate**: Full cutover, no canary

### Automatic Rollback

Monitors live metrics and rolls back if:
- Error rate exceeds threshold (default 5%)
- Latency exceeds threshold (default 150ms at p95)
- Connection resets exceed threshold (default 1%)
- Custom metrics deviate beyond tolerance

## Features

✅ **Single unified CRD** - All-in-one like Deployment
✅ **Automatic conversion** - Parse ingress, generate gateway API resources
✅ **Multi-gateway support** - Envoy, Istio, Kong, AWS ALB, GCP Cloud Armor
✅ **Canary deployments** - Gradual traffic shift with health monitoring
✅ **Auto-rollback** - Metrics-based with configurable thresholds
✅ **Annotation mapping** - Convert nginx → gateway policies
✅ **Multi-team coordination** - Namespace selectors, independent migrations
✅ **Lifecycle hooks** - Pre/post conversion and rollout webhooks
✅ **Audit trail** - Complete migration history and status tracking
✅ **Kubernetes native** - CRD, webhooks, RBAC, events

## Getting Started

### 1. Install Prerequisites

```bash
# Kubernetes 1.26+
kubectl version

# Install Gateway API CRDs
kubectl get crd gatewayclasses.gateway.networking.k8s.io || \
  kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml

# Install target gateway (e.g., Envoy)
helm repo add envoy-gateway https://envoyproxy.io/charts
helm install eg envoy-gateway/gateway -n envoy-gateway-system --create-namespace
```

### 2. Install TransferGW Operator

```bash
# Clone or download
git clone https://github.com/thev1ndu/transfergw.git
cd transfergw

# Build operator image
docker build -t my-registry/transfergw-controller:latest .
docker push my-registry/transfergw-controller:latest

# Install CRD, RBAC, operator
kubectl apply -f crd.yaml
kubectl apply -f rbac.yaml
sed 's|transfergw-controller:latest|my-registry/transfergw-controller:latest|' operator-deployment.yaml | kubectl apply -f -
```

### 3. Create Migration

```bash
# Label ingresses to migrate
kubectl label ingress my-ingress -n prod migrate=true

# Apply migration resource
kubectl apply -f example-simple.yaml

# Monitor progress
watch kubectl get transfergws -A
kubectl describe transfergw simple-prod-migration -n transfergw
```

## Testing

Run unit tests:
```bash
go test ./... -v
```

Run integration tests:
```bash
make test-integration
```

Run end-to-end tests:
```bash
make test-e2e
```

## Development

### Build Operator

```bash
make build
# Output: bin/manager
```

### Run Locally

```bash
make run
```

### Generate Code

```bash
make generate  # Generate code from CRD types
make manifests # Generate CRD YAML from Go structs
```

### Debugging

Enable debug logging:
```bash
kubectl patch deployment transfergw-controller -n transfergw --type merge \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"controller","env":[{"name":"LOG_LEVEL","value":"debug"}]}]}}}}'

kubectl logs -f -n transfergw deployment/transfergw-controller
```

## API Reference

### TransferGW Spec

**selector** (required)
- `namespaces`: Array of namespace patterns (glob supported)
- `ingressSelector`: Label selectors for ingress resources
- `ingressClasses`: Specific ingress classes to migrate

**conversion** (required)
- `gatewayClass`: Target gateway class (envoy, istio, kong, aws-alb, gcp-cloud-armor)
- `targetNamespace`: Where gateway resources are created (default: transfergw)
- `generateGateway`: Auto-create Gateway if missing (default: true)
- `annotationPolicy`: Preserve/translate/drop ingress annotations
- `tlsHandling`: preserve, regenerate, or manual TLS handling
- `validationMode`: strict, permissive, or disabled

**rollout** (required)
- `mode`: immediate, gradual, or canary
- `strategy`: percentage, time-based, or manual
- `canary`: Canary-specific settings (percentage, duration, increment)
- `gradual`: Gradual settings (total duration, step size)
- `paused`: Pause rollout without deletion

**monitoring**
- `enabled`: Enable health checks (default: true)
- `metrics`: [latency, error-rate, throughput, connection-reset]
- `thresholds`: Error rate, latency, connection resets, throughput
- `alerting`: Slack, webhooks for anomalies

**lifecycle**
- `pauseOriginalIngress`: Pause old ingress during canary
- `backupOriginal`: Keep backup of original configuration
- `validateConversion`: Reject migrations with warnings
- `autoComplete`: Mark complete when reaching 100%
- `cleanupOnSuccess`: Delete original ingress after migration
- `hooks`: Pre/post conversion and rollout webhooks

### TransferGW Status

**phase**: Pending, Analyzing, Converting, Deploying, Canary, Complete, Failed
**completionPercentage**: 0-100 traffic percentage on gateway
**trafficRouting**: Current split between ingress and gateway
**processed**: Counts of total/converted/pending/failed ingresses
**resources**: Created gateways, routes, policies
**metrics**: Latency, error-rate, throughput comparison
**issues**: Conversion warnings and recommendations
**conditions**: Standard Kubernetes conditions

## Examples

See `example-simple.yaml` and `example-complex.yaml` for complete working examples covering:
- Simple 80/20 ingress with canary
- Complex 500+ rule enterprise migration
- Annotation translation and policies
- Multi-team namespace coordination
- Slack alerting and webhooks

## Troubleshooting

**Ingresses not being converted**
```bash
kubectl get ingress -A -L migrate
# Verify labels/namespaces match selector
```

**Operator not starting**
```bash
kubectl logs -n transfergw deployment/transfergw-controller
# Check: CRD installed, RBAC created, image available
```

**Metrics not comparing**
```bash
kubectl get service -n transfergw transfergw-controller-metrics
# Verify Prometheus scraping both ingress and gateway services
```

## Contributing

1. Fork and clone
2. Create feature branch: `git checkout -b feature/my-feature`
3. Write tests
4. Submit pull request

## License

Apache License 2.0

## Support

- Docs: IDEA.md (architecture), SETUP.md (installation), README.md (overview)
- Issues: GitHub issue tracker
- Examples: example-simple.yaml, example-complex.yaml

## Roadmap

- [ ] Support for other K8s API groups (RBAC, OIDC)
- [ ] Multi-cluster migrations
- [ ] Cost optimization recommendations
- [ ] Prometheus operator integration
- [ ] ArgoCD/Flux native support
- [ ] Migration validation test suite

# Gateway Migration Framework: Ingress to Gateway API

## Problem Statement

Enterprises running Kubernetes clusters face significant operational friction when modernizing their networking layer. Current state:

1. **Legacy Ingress Controllers** are limited in expressiveness, lack cross-namespace configuration, and provide no native traffic splitting or weighted routing at scale
2. **Complex Migration Path** requires rewriting ingress manifests, managing compatibility layers, and coordinating rollouts across multiple teams
3. **Manual Labor** each ingress rule must be individually analyzed, mapped to Gateway API constructs, and tested
4. **Risk of Downtime** during deployment without gradual migration strategy or rollback mechanism
5. **Vendor Lock-in** ingress configurations often encode controller-specific annotations, making portability difficult

Typical enterprises have 50-500+ ingress resources with mixed patterns (HTTPS termination, host-based routing, path-based routing, request modification, rate limiting). A single complex ingress might map to multiple Gateway API resources (Gateway, HTTPRoute, TLSPolicy).

## Solution: TransferGW CRD (All-in-One)

A single unified Kubernetes CRD that manages the entire ingress-to-gateway migration lifecycle. Think of it like Deployment for migrations: one resource that declares intent, manages conversion, orchestrates rollout, and monitors health.

## TransferGW CRD

```yaml
apiVersion: gateway.example.com/v1alpha1
kind: TransferGW
metadata:
  name: prod-migration
  namespace: gateway-system
spec:
  # SELECTION: Which Ingresses to migrate
  selector:
    namespaces: ["prod-*", "staging"]
    ingressSelector:
      matchLabels:
        migrate: "true"
  
  # CONVERSION: How to translate to Gateway API
  conversion:
    gatewayClass: envoy  # or istio, kong, aws-alb
    generateGateway: true  # create Gateway resource if not exists
    annotationPolicy:
      preserve:
        - "cert-manager.io/.*"
        - "auth.example.com/.*"
      translate:
        "nginx.ingress.kubernetes.io/rate-limit": "gateway.example.com/rate-limit"
    tlsHandling: preserve  # preserve certs, domains, TLS config
  
  # ROLLOUT: Gradual migration strategy
  rollout:
    mode: canary  # immediate, gradual, canary
    strategy: percentage  # percentage, time-based, manual
    canary:
      percentage: 25
      duration: 1h
      increment: 25  # increase by 25% every hour
      maxIncrement: 50  # cap increase at 50% per step
  
  # MONITORING: Health checks and rollback
  monitoring:
    enabled: true
    metrics:
      - latency
      - error-rate
      - throughput
      - connection-reset
    thresholds:
      errorRate: 0.05  # rollback if > 5% error rate
      latency: 150ms  # rollback if median latency > 150ms
      connectionResets: 0.01  # rollback if > 1% connection resets
    comparisonWindow: 5m  # evaluate metrics over last 5 minutes
  
  # LIFECYCLE: Cleanup and validation
  lifecycle:
    pauseOriginalIngress: true  # pause old ingress during canary
    backupOriginal: true  # keep ingress resource as backup
    validateConversion: true  # reject if conversion has warnings
    autoComplete: true  # mark as complete when traffic reaches 100%

status:
  # OBSERVATION: Current migration state
  phase: Canary  # Pending, Analyzing, Converting, Deploying, Canary, Complete, Failed
  completionPercentage: 35
  
  # TRAFFIC DISTRIBUTION
  trafficRouting:
    ingress: 65%
    gateway: 35%
  
  # INGRESSES PROCESSED
  processed:
    total: 124
    converted: 87
    pending: 25
    failed: 12
  
  # GATEWAY RESOURCES CREATED
  resources:
    gateways: 8
    httpRoutes: 87
    tlsPolicies: 12
  
  # LIVE METRICS COMPARISON
  metrics:
    latency:
      ingress: 45ms
      gateway: 48ms
      delta: "+3ms"
      status: "OK"
    errorRate:
      ingress: 0.0008
      gateway: 0.0009
      delta: "+0.0001"
      status: "OK"
    throughput:
      ingress: "2.4 megabits per second"
      gateway: "2.35 megabits per second"
      delta: "-50 kilobits per second"
      status: "OK"
  
  # ISSUES AND ACTIONS
  issues:
    - ingress: "api-v2/ingress-stripe"
      issue: "regex path not supported in HTTPRoute"
      severity: warning
      recommendation: "rewrite path pattern or use path prefix"
    - ingress: "payment/ingress-internal"
      issue: "custom annotation 'auth/ldap-group' not recognized"
      severity: info
      recommendation: "use auth.example.com/group policy instead"
  
  # ROLLBACK READINESS
  rollbackReady: true
  lastHealthCheck: "2024-09-11T15:23:45Z"
  nextHealthCheck: "2024-09-11T15:28:45Z"
  
  # CONDITIONS
  conditions:
    - type: AnalysisComplete
      status: "True"
      lastTransitionTime: "2024-09-11T12:00:00Z"
    - type: ConversionComplete
      status: "True"
      lastTransitionTime: "2024-09-11T13:15:00Z"
    - type: CanaryHealthy
      status: "True"
      lastTransitionTime: "2024-09-11T14:00:00Z"
    - type: Ready
      status: "False"
      reason: "CanaryInProgress"
```

## How It Works

### Phase 1: Analyze
Operator scans all matching Ingress resources.
- Detects routing patterns, TLS certificates, annotations
- Identifies unsupported features requiring manual review
- Computes complexity score and conversion confidence
- Generates conversion report with warnings

### Phase 2: Convert
Operator generates Gateway API resources from Ingresses.
- Creates Gateway resource (if needed)
- Generates HTTPRoute for each Ingress rule
- Translates annotations to Gateway policies
- Preserves TLS certificates and domains
- Stores original Ingress as backup annotation

### Phase 3: Deploy
Operator deploys generated Gateway resources to cluster.
- Creates HTTPRoute and TLSPolicy manifests
- Configures traffic split at load balancer level
- Initializes canary with configured percentage

### Phase 4: Monitor
Operator continuously compares old vs new routing.
- Collects metrics from both paths
- Compares latency, error rates, throughput
- Detects anomalies and alerts on deviations
- Automatically rolls back if thresholds breached

### Phase 5: Complete
Traffic reaches 100% on Gateway API.
- Marks ingress resources as migrated
- Removes backup ingress if cleanup enabled
- Updates status to Complete

## Key Features

**Single Resource** Declare entire migration in one CRD, similar to Deployment.

**Automatic Conversion** Parse Ingress and generate equivalent Gateway API resources, handling edge cases like regex paths and weighted backends.

**Canary Deployment** Route configurable percentage of traffic to new Gateway. Automatically increment over time or manually advance.

**Live Metrics Comparison** Side-by-side comparison of latency, error rates, throughput. Automatic rollback if metrics deviate.

**Annotation Translation** Convert controller-specific annotations to gateway policies. Preserve important metadata like cert-manager integration.

**Multi-Team** Namespace-level selection allows teams to migrate independently without cluster-wide coordination.

**Rollback Safety** Pause original Ingress during canary, backup configuration, automatic rollback on health check failure.

**Visibility** Real-time status updates, conversion warnings, issue tracking, metrics dashboards.

## Example Workflows

### Workflow 1: Canary Rollout
```bash
# 1. Create TransferGW resource
kubectl apply -f transfer-gw.yaml

# 2. Operator analyzes and converts
# Status: Analyzing → Converting → Deploying

# 3. Canary phase starts (25% traffic to gateway)
# Status: Canary, completionPercentage: 25

# 4. Metrics look good, auto-increment to 50%
# Status: Canary, completionPercentage: 50

# 5. After 1 hour and all thresholds met, complete migration
# Status: Complete
```

### Workflow 2: Manual Halt and Rollback
```bash
# If metrics exceed threshold, operator pauses and rolls back
kubectl get TransferGW prod-migration
# Phase: Canary (paused due to error-rate threshold)
# completionPercentage: 45

# Investigate issue, then resume
kubectl patch TransferGW prod-migration --type merge -p '{"spec":{"rollout":{"paused":false}}}'
```

### Workflow 3: Immediate Deployment
```yaml
spec:
  rollout:
    mode: immediate  # no canary, full deployment
```

## Technical Architecture

```
┌─────────────────────────────────────┐
│   TransferGW Controller             │
└─────────────────────────────────────┘
        ↓              ↓              ↓
    Analyzer      Converter       Monitor
      │              │              │
      ├─→ Scan selected Ingress resources
      │
      ├─→ Generate HTTPRoute + Gateway manifests
      │
      ├─→ Deploy resources to cluster
      │
      ├─→ Configure traffic split (percentage routing)
      │
      └─→ Continuous health check + auto-rollback

Status updates flow back to status.phase, status.metrics, status.conditions
```

## Implementation Components

### Operator (Golang)
- TransferGW CRD controller
- Conversion engine (Ingress → Gateway API)
- Traffic management controller (percentage routing)
- Metrics collector and comparator
- Automatic rollback logic

### Data Store
- Backup original Ingress config as annotation
- Store conversion issues and recommendations
- Audit trail of all migration events

### Integration Points
- Kubernetes API (TransferGW, Ingress, Gateway, HTTPRoute)
- Load balancer (traffic percentage routing via Envoy, Istio, etc.)
- Metrics backend (Prometheus for latency, error rates)
- Alert system (notify on anomalies, rollback events)

## Benefits

1. **Simplified Operations** single resource replaces complex multi-step process
2. **Risk Reduction** automatic rollback and canary deployment prevent outages
3. **Time Savings** automatic conversion reduces weeks of manual work
4. **Auditability** complete audit trail of routing changes
5. **Scalability** handles 50-500+ ingresses without manual intervention

## Success Metrics

- Completion rate (% of ingresses successfully migrated)
- Rollback frequency and root causes
- Migration time per ingress (target: less than 5 minutes)
- Anomaly detection accuracy (false positive rate less than 2%)
- Operator API latency (p99 less than 100 milliseconds)

# TransferGW Setup Guide

## Prerequisites

- Kubernetes 1.26+
- Gateway API v1beta1 installed
- Target gateway controller installed (Envoy, Istio, Kong, etc.)
- kubectl configured with cluster-admin access
- Docker (for building operator image)

## Architecture Overview

TransferGW operates as a Kubernetes operator that:
1. Watches Ingress resources matching selection criteria
2. Converts Ingress manifests to Gateway API HTTPRoute/Gateway resources
3. Manages gradual traffic migration with canary deployments
4. Monitors health metrics and auto-rollbacks on threshold breach
5. Maintains audit trail and supports pre/post migration hooks

## Installation

### Step 1: Install CRD

```bash
kubectl apply -f crd.yaml
```

Verify CRD is installed:
```bash
kubectl get crd transfergws.gateway.example.com
```

### Step 2: Create Namespace

```bash
kubectl create namespace transfergw
kubectl label namespace transfergw control-plane=controller-manager
```

### Step 3: Apply RBAC

```bash
kubectl apply -f rbac.yaml
```

### Step 4: Build and Push Operator Image

```bash
docker build -t transfergw-controller:latest .
docker push <registry>/transfergw-controller:latest
```

Update `operator-deployment.yaml` image reference if using private registry.

### Step 5: Deploy Operator

```bash
kubectl apply -f operator-deployment.yaml
```

Verify operator is running:
```bash
kubectl get deployment -n transfergw transfergw-controller
kubectl logs -n transfergw deployment/transfergw-controller
```

### Step 6: Install Target Gateway

Install your target gateway. Envoy Gateway is recommended (default in examples):

**Envoy Gateway (Recommended):**
```bash
# Add Helm repository
helm repo add envoy-gateway https://envoyproxy.io/charts
helm repo update

# Install Envoy Gateway
helm install eg envoy-gateway/gateway \
  -n envoy-gateway-system --create-namespace \
  --set config.envoyGateway.logging.level.default=info

# Verify installation
kubectl get deployment -n envoy-gateway-system
kubectl get gatewayclass envoy
```

**Istio (Alternative):**
```bash
# Download and install Istio
curl -L https://istio.io/downloadIstio | sh -
cd istio-*/
export PATH=$PWD/bin:$PATH

# Install with demo profile
istioctl install --set profile=demo -y

# Verify installation
kubectl get gatewayclass istio
```

**Kong Gateway (Alternative):**
```bash
helm repo add kong https://charts.konghq.com
helm repo update

helm install kong kong/ingress -n kong --create-namespace

# Verify installation
kubectl get gatewayclass kong
```

**AWS ALB Gateway (For EKS):**
```bash
# Install AWS Gateway API controller
helm repo add aws-observability https://aws.github.io/aws-observability-helm-charts
helm repo update

helm install aws-gateway-controller aws-observability/aws-gateway-controller \
  -n kube-system

# Verify installation
kubectl get gatewayclass aws-alb
```

## Quick Start

### 1. Label Ingresses to Migrate

```bash
kubectl label ingress my-ingress -n prod migrate=true
```

### 2. Create TransferGW Resource

Simple migration (25% canary, 1h increments):
```bash
kubectl apply -f example-simple.yaml
```

Complex migration (with annotation mapping, hooks):
```bash
kubectl apply -f example-complex.yaml
```

### 3. Monitor Progress

Watch real-time status:
```bash
watch kubectl get transfergws -A
```

Get detailed status:
```bash
kubectl describe transfergw simple-prod-migration -n transfergw
```

View controller logs:
```bash
kubectl logs -f -n transfergw deployment/transfergw-controller
```

## Configuration

### Selector Options

Select ingresses by namespace patterns and labels:

```yaml
selector:
  namespaces: ["prod-*", "staging"]  # glob patterns supported
  ingressSelector:
    matchLabels:
      migrate: "true"
    matchExpressions:
      - key: team
        operator: In
        values: ["platform", "api"]
  ingressClasses: ["nginx", "haproxy"]  # specific classes
```

### Conversion Options

Map annotations and handle TLS:

```yaml
conversion:
  gatewayClass: envoy
  targetNamespace: transfergw
  generateGateway: true
  annotationPolicy:
    preserve: ["cert-manager.io/.*"]
    translate:
      "nginx.ingress.kubernetes.io/rate-limit": "gateway.example.com/rate-limit"
    drop: ["deprecated-.*"]
  tlsHandling: preserve  # or regenerate, manual
```

### Rollout Strategies

**Canary (default):**
```yaml
rollout:
  mode: canary
  canary:
    initialPercentage: 25
    increment: 25
    stepDuration: 1h
    maxDuration: 4h
```

**Gradual:**
```yaml
rollout:
  mode: gradual
  gradual:
    totalDuration: 48h
    step: 10  # percentage per step
```

**Immediate:**
```yaml
rollout:
  mode: immediate  # full cutover, no canary
```

### Monitoring and Rollback

```yaml
monitoring:
  enabled: true
  interval: 5m
  metrics: ["latency", "error-rate", "throughput"]
  thresholds:
    errorRate: 0.05  # 5%
    latencyMs: 150
    latencyPercentile: p95
    connectionResets: 0.01
  alerting:
    enabled: true
    slackChannel: "#migrations"
    webhookUrl: "https://alerts.example.com/webhook"
```

### Lifecycle Hooks

Run custom logic at migration stages:

```yaml
lifecycle:
  hooks:
    preConversion: https://hooks.example.com/pre-conversion
    postConversion: https://hooks.example.com/post-conversion
    preRollout: https://hooks.example.com/pre-rollout
    postRollout: https://hooks.example.com/post-rollout
```

Webhook receives POST with migration context:
```json
{
  "phase": "preConversion",
  "migration": "simple-prod-migration",
  "namespace": "transfergw",
  "ingressesCount": 42
}
```

## Common Operations

### Pause Migration

```bash
kubectl patch transfergw simple-prod-migration -n transfergw \
  --type merge -p '{"spec":{"rollout":{"paused":true}}}'
```

### Resume Migration

```bash
kubectl patch transfergw simple-prod-migration -n transfergw \
  --type merge -p '{"spec":{"rollout":{"paused":false}}}'
```

### Manually Advance Canary

```bash
kubectl patch transfergw simple-prod-migration -n transfergw \
  --type merge -p '{"spec":{"rollout":{"strategy":"manual"}}}'
```

Then update percentage:
```bash
kubectl patch transfergw simple-prod-migration -n transfergw \
  --type merge -p '{"status":{"completionPercentage":50}}'
```

### Rollback to Ingress

```bash
kubectl patch transfergw simple-prod-migration -n transfergw \
  --type merge -p '{"spec":{"rollout":{"mode":"rollback"}}}'
```

### View Conversion Issues

```bash
kubectl get transfergw simple-prod-migration -n transfergw -o jsonpath='{.status.issues}' | jq
```

## Gateway-Specific Configuration

### Envoy Gateway Migration

Example for Envoy Gateway target:

```yaml
apiVersion: gateway.example.com/v1beta1
kind: TransferGW
metadata:
  name: envoy-migration
  namespace: transfergw
spec:
  selector:
    namespaces: ["prod"]
    ingressSelector:
      matchLabels:
        migrate: "true"
  
  conversion:
    gatewayClass: envoy
    targetNamespace: envoy-gateway-system
    generateGateway: true
    tlsHandling: preserve
  
  rollout:
    mode: canary
    canary:
      initialPercentage: 25
      increment: 25
      stepDuration: 1h
  
  monitoring:
    enabled: true
    metrics: ["latency", "error-rate"]
    thresholds:
      errorRate: 0.05
      latencyMs: 150
```

### Istio Migration

Example for Istio target:

```yaml
apiVersion: gateway.example.com/v1beta1
kind: TransferGW
metadata:
  name: istio-migration
  namespace: transfergw
spec:
  selector:
    namespaces: ["prod", "staging"]
  
  conversion:
    gatewayClass: istio
    targetNamespace: istio-system
    annotationPolicy:
      translate:
        "nginx.ingress.kubernetes.io/rate-limit": "gateway.example.com/rate-limit"
  
  rollout:
    mode: gradual
    gradual:
      totalDuration: 48h
      step: 10
```

### Kong Gateway Migration

Example for Kong Gateway target:

```yaml
spec:
  conversion:
    gatewayClass: kong
    annotationPolicy:
      preserve: ["kong.ingress.kubernetes.io/.*"]
```

## Troubleshooting

### Controller Not Starting

Check logs:
```bash
kubectl logs -n transfergw deployment/transfergw-controller
```

Common issues:
- Missing CRD: Run `kubectl apply -f crd.yaml`
- RBAC missing: Run `kubectl apply -f rbac.yaml`
- Image pull failed: Check image reference and registry credentials

### Webhook Certificate Errors

Webhook certificates are auto-generated. If errors persist:

```bash
kubectl delete secret transfergw-webhook-certs -n transfergw
kubectl rollout restart deployment/transfergw-controller -n transfergw
```

### Ingresses Not Being Converted

Check selector matches:
```bash
kubectl get ingress --all-namespaces -L migrate
```

Verify labels/namespaces match TransferGW selector spec.

### Metrics Not Comparing

Ensure metrics collector is scraping both ingress and gateway:

```bash
kubectl get service -n transfergw transfergw-controller-metrics
```

## Development

### Build Operator

```bash
go mod tidy
go build -o bin/manager main.go
```

### Run Locally

```bash
go run main.go --leader-elect=false
```

### Build Docker Image

```bash
docker build -t transfergw-controller:dev .
```

### Run Tests

```bash
go test ./...
```

## Uninstall

```bash
# Delete all TransferGW migrations
kubectl delete transfergws --all-namespaces

# Delete operator
kubectl delete deployment transfergw-controller -n transfergw

# Delete RBAC
kubectl delete sa,clusterrole,clusterrolebinding,role,rolebinding -n transfergw -l app=transfergw

# Delete CRD (WARNING: deletes all migration history)
kubectl delete crd transfergws.gateway.example.com
```

## Support

- Issues: GitHub issue tracker
- Docs: IDEA.md for architecture overview
- Examples: example-simple.yaml, example-complex.yaml

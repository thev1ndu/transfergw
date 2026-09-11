# TransferGW Installation Guide

## Prerequisites

- Kubernetes cluster 1.26+
- kubectl configured to access cluster
- Helm 3+ installed
- Container image built and available: `transfergw-controller:latest`

## Quick Start

### 1. Build Container Image

```bash
make docker-build
# or manually:
docker build -t transfergw-controller:latest .
```

Load image to cluster (for local testing):

```bash
kind load docker-image transfergw-controller:latest --name <cluster-name>
```

### 2. Install via Helm

```bash
helm install transfergw chart/transfergw \
  --namespace gateway-system \
  --create-namespace
```

### 3. Verify Installation

```bash
kubectl get deployment -n gateway-system
kubectl get crd transfergw.t-1.dev
kubectl get pods -n gateway-system -l app=transfergw-controller
```

## Configuration

### Custom Image

```bash
helm install transfergw chart/transfergw \
  --namespace gateway-system \
  --set image.repository=myregistry/transfergw-controller \
  --set image.tag=v1.0.0 \
  --set image.pullPolicy=Always
```

### Production Settings

```bash
helm install transfergw chart/transfergw \
  --namespace gateway-system \
  --set replicaCount=3 \
  --set resources.requests.cpu=200m \
  --set resources.requests.memory=256Mi \
  --set resources.limits.cpu=1000m \
  --set resources.limits.memory=1Gi \
  --set leaderElection.enabled=true
```

### Skip CRD Installation (if already installed)

```bash
helm install transfergw chart/transfergw \
  --namespace gateway-system \
  --set crd.install=false
```

### Disable Webhooks

```bash
helm install transfergw chart/transfergw \
  --namespace gateway-system \
  --set enableWebhooks=false
```

## Creating a Migration

After installation, create a TransferGW resource:

```yaml
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: my-migration
  namespace: gateway-system
spec:
  selector:
    namespaces:
      - "production-*"
    ingressSelector:
      matchLabels:
        migrate: "true"
  conversion:
    gatewayClass: nginx
    targetNamespace: gateway-system
    generateGateway: true
  rollout:
    mode: canary
```

Apply it:

```bash
kubectl apply -f migration.yaml
```

Monitor progress:

```bash
kubectl get transfergw my-migration -n gateway-system -w
kubectl describe transfergw my-migration -n gateway-system
```

## Troubleshooting

### Check Logs

```bash
kubectl logs -n gateway-system \
  -l app=transfergw-controller \
  -f
```

### Check RBAC

```bash
kubectl get clusterrole transfergw-controller
kubectl get clusterrolebinding transfergw-controller
kubectl get role -n gateway-system transfergw-controller
kubectl get rolebinding -n gateway-system transfergw-controller
```

### Check Service Account

```bash
kubectl get sa -n gateway-system transfergw-controller
```

### Webhook Issues

Check webhook configurations:

```bash
kubectl get validatingwebhookconfigurations
kubectl get mutatingwebhookconfigurations
```

View webhook logs:

```bash
kubectl logs -n gateway-system deployment/transfergw-controller --tail=100 | grep webhook
```

## Uninstall

```bash
# Delete all TransferGW resources first
kubectl delete transfergw --all -A

# Uninstall Helm release
helm uninstall transfergw -n gateway-system

# Optionally remove namespace
kubectl delete namespace gateway-system
```

## Advanced Configuration

See `chart/transfergw/values.yaml` for all available options.

Create custom values file:

```yaml
# custom-values.yaml
replicaCount: 3
logLevel: debug
resources:
  limits:
    memory: 2Gi
    cpu: 2000m
```

Install with custom values:

```bash
helm install transfergw chart/transfergw \
  --namespace gateway-system \
  --values custom-values.yaml
```

# TransferGW Deployment Guide

## Published Artifacts

### Container Image
- **Registry:** GitHub Container Registry (GHCR)
- **Repository:** `ghcr.io/thev1ndu/transfergw`
- **Tags:** `latest`, git commit SHA, version tags (v*)
- **Built via:** GitHub Actions workflow on every push to main

### Helm Chart
- **Registry:** GHCR (OCI format)
- **Repository:** `oci://ghcr.io/thev1ndu/helm-charts/transfergw`
- **Published via:** GitHub Actions workflow

## Deploy to Kubernetes

### Prerequisites

1. Kubernetes cluster 1.26+
2. kubectl configured
3. Helm 3+
4. GitHub Container Registry credentials (if using private repos)

### Quick Deploy (Recommended)

```bash
# Add Helm repository (if using HTTP index)
helm repo add transfergw-charts https://thev1ndu.github.io/transfergw
helm repo update

# Or use OCI registry directly
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --namespace gateway-system \
  --create-namespace \
  --version latest
```

### Deploy with Custom Values

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --namespace gateway-system \
  --create-namespace \
  --set image.repository=ghcr.io/thev1ndu/transfergw \
  --set image.tag=latest \
  --set replicaCount=3 \
  --set resources.limits.memory=2Gi
```

### Production Deployment

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --namespace gateway-system \
  --create-namespace \
  -f chart/transfergw/examples/values-prod.yaml \
  --set image.repository=ghcr.io/thev1ndu/transfergw \
  --set image.tag=latest
```

### Development Deployment

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --namespace gateway-system \
  --create-namespace \
  -f chart/transfergw/examples/values-dev.yaml \
  --set image.repository=ghcr.io/thev1ndu/transfergw \
  --set image.tag=latest
```

## Verify Deployment

```bash
# Check deployment
kubectl get deployment -n gateway-system

# Check pods
kubectl get pods -n gateway-system -l app=transfergw-controller

# Check CRD
kubectl get crd transfergw.t-1.dev

# View controller logs
kubectl logs -n gateway-system -l app=transfergw-controller -f
```

## Access Controller Metrics

```bash
# Forward metrics port
kubectl port-forward -n gateway-system svc/transfergw-controller-metrics 8080:8080

# View metrics
curl http://localhost:8080/metrics
```

## Create First Migration

```bash
kubectl apply -f - <<EOF
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: production-migration
  namespace: gateway-system
spec:
  selector:
    namespaces:
      - production
    ingressSelector:
      matchLabels:
        migrate: "true"
  conversion:
    gatewayClass: nginx
    targetNamespace: gateway-system
    generateGateway: true
  rollout:
    mode: canary
    strategy: percentage
EOF
```

Monitor the migration:

```bash
# Watch status
kubectl get transfergw production-migration -n gateway-system -w

# Detailed status
kubectl describe transfergw production-migration -n gateway-system

# Follow logs
kubectl logs -n gateway-system -l app=transfergw-controller -f
```

## Troubleshooting

### Pod not starting

```bash
kubectl logs -n gateway-system -l app=transfergw-controller --all-containers=true
kubectl describe pod -n gateway-system -l app=transfergw-controller
```

### Image pull errors

Check if GHCR credentials are configured:

```bash
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password=<github-token> \
  --docker-email=<email> \
  -n gateway-system
```

Then update the deployment to use this secret:

```bash
kubectl patch sa transfergw-controller -n gateway-system -p '{"imagePullSecrets": [{"name": "ghcr-secret"}]}'
```

### Check webhook issues

```bash
kubectl get validatingwebhookconfigurations
kubectl get mutatingwebhookconfigurations
kubectl logs -n gateway-system -l app=transfergw-controller | grep webhook
```

## Uninstall

```bash
helm uninstall transfergw -n gateway-system
kubectl delete namespace gateway-system
```

## CI/CD Pipeline Status

Check GitHub Actions for build status:
- https://github.com/thev1ndu/transfergw/actions

Latest builds and deployments automatically trigger when pushing to main branch.

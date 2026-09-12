# TransferGW Helm Chart

Kubernetes operator for migrating Ingress to Gateway APIs with SIG-Network compliant design.

## Installation

### Prerequisites

- Kubernetes 1.26+
- Helm 3+
- Gateway API CRDs (installed separately or via this chart)

### Add Helm Repository

```bash
helm repo add transfergw https://charts.example.com
helm repo update
```

### Install Chart

```bash
helm install transfergw chart/transfergw -n transfergw --create-namespace
```

### Custom Values

```bash
helm install transfergw chart/transfergw \
  --namespace transfergw \
  --create-namespace \
  --values custom-values.yaml
```

## Configuration

### Key Values

| Key | Default | Description |
|-----|---------|-------------|
| `namespace` | `transfergw` | Kubernetes namespace |
| `createNamespace` | `true` | Create namespace if not exists |
| `image.repository` | `transfergw-controller` | Container image |
| `image.tag` | `latest` | Image tag |
| `replicaCount` | `2` | Number of replicas |
| `logLevel` | `info` | Log level |
| `enableWebhooks` | `true` | Enable webhook validation |
| `rbac.create` | `true` | Create RBAC resources |
| `serviceAccount.create` | `true` | Create service account |
| `crd.install` | `true` | Install CRDs |

### Resource Configuration

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

### High Availability

For production deployments:

```bash
helm install transfergw chart/transfergw \
  --set replicaCount=3 \
  --set leaderElection.enabled=true \
  --set podDisruptionBudget.enabled=true \
  --set affinity.podAntiAffinity.preferredDuringSchedulingIgnoredDuringExecution[0].weight=100
```

## Usage

Create a TransferGW resource:

```yaml
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: ingress-migration
  namespace: transfergw
spec:
  selector:
    namespaces:
      - production-*
      - staging
    ingressClasses:
      - nginx
  conversion:
    gatewayClass: nginx
    targetNamespace: transfergw
    generateGateway: true
  rollout:
    mode: canary
    strategy: percentage
```

## Uninstall

```bash
helm uninstall transfergw -n transfergw
```

## Troubleshooting

### Check Controller Logs

```bash
kubectl logs -n transfergw deployment/transfergw-controller -f
```

### Check CRD Installation

```bash
kubectl get crd transfergws.transfergw.t-1.dev
```

### Check RBAC Permissions

```bash
kubectl get clusterrole transfergw-controller
kubectl get clusterrolebinding transfergw-controller
```

## License

Apache 2.0

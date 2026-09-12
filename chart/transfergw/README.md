# TransferGW

Kubernetes operator that migrates `Ingress` resources to the [Gateway API](https://gateway-api.sigs.k8s.io/)
without a rip-and-replace cutover. A single `TransferGW` custom resource selects a set
of Ingresses, converts them into `Gateway`/`HTTPRoute` objects, and shifts traffic to
the new stack at a controlled percentage — with the original Ingress left untouched
until you're satisfied and cut over yourself (DNS/LB change).

## Prerequisites

- Kubernetes 1.26+
- Helm 3+
- [Gateway API CRDs](https://gateway-api.sigs.k8s.io/guides/#installing-gateway-api) installed
- A Gateway API implementation running in the cluster (e.g. [Envoy Gateway](https://gateway.envoyproxy.io/),
  Istio, or another controller with a `GatewayClass` already registered)

## Install

```bash
helm repo add transfergw https://thev1ndu.github.io/transfergw
helm repo update

helm install transfergw transfergw/transfergw \
  --namespace transfergw --create-namespace
```

Or pull directly from the OCI registry instead of adding a classic repo:

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --version 1.0.0 --namespace transfergw --create-namespace
```

Verify it's running:

```bash
kubectl rollout status deployment/transfergw-controller -n transfergw
kubectl get crd transfergws.transfergw.t-1.dev
```

### Values

| Key | Default | Description |
|---|---|---|
| `image.repository` | `thev1ndu/transfergw` | Controller image (Docker Hub) |
| `image.tag` | `latest` | Image tag — pin to a `sha-<commit>` tag for reproducible installs |
| `replicaCount` | `2` | Controller replicas |
| `logLevel` | `info` | Controller log level |
| `alerting.enabled` | `false` | Cluster-wide default webhook, notified on a health rollback for any `TransferGW` that leaves `spec.monitoring.alerting` unset |
| `alerting.webhookUrl` | `""` | Webhook URL used when `alerting.enabled` is `true` |
| `rbac.create` | `true` | Create the ClusterRole/ClusterRoleBinding |
| `serviceAccount.create` | `true` | Create the ServiceAccount |
| `crd.install` | `true` | Install the `TransferGW` CRD |
| `podDisruptionBudget.enabled` | `true` | Create a PodDisruptionBudget |

Override any of these with `--set` or `-f custom-values.yaml`, e.g.:

```bash
helm install transfergw transfergw/transfergw \
  --namespace transfergw --create-namespace \
  --set image.tag=sha-a8fe786 \
  --set replicaCount=3
```

## Run a migration

Find the `GatewayClass` you're migrating onto:

```bash
kubectl get gatewayclass
```

Label the Ingresses you want migrated, then apply a `TransferGW`:

```bash
kubectl label ingress my-ingress -n my-namespace migrate=true

kubectl apply -f - <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: my-migration
  namespace: my-namespace
spec:
  selector:
    namespaces: [my-namespace]
    ingressSelector: {matchLabels: {migrate: "true"}}
  conversion:
    gatewayClass: <your-gatewayclass>
    generateGateway: true
  rollout:
    mode: immediate
EOF
```

`generateGateway: true` (the default) has TransferGW create its own `Gateway` named
`<migration-name>-gateway`. If you already have a Gateway you want to attach to instead
(e.g. one shared Gateway holding your only public LoadBalancer IP), set
`generateGateway: false` and `gatewayName: <existing-gateway-name>` instead — see
[docs/RUNBOOK.md](https://github.com/thev1ndu/transfergw/blob/main/docs/RUNBOOK.md) for
a worked example of that exact scenario.

Check progress:

```bash
kubectl get transfergw my-migration -n my-namespace -w
kubectl describe transfergw my-migration -n my-namespace
kubectl get httproute -n my-namespace
```

`rollout.mode: immediate` moves straight to 100%; `canary` or `gradual` ramp up over
time instead — see [docs/SETUP.md](https://github.com/thev1ndu/transfergw/blob/main/docs/SETUP.md)
for the full `spec` reference (rollout strategies, health-based automatic rollback,
annotation handling, alerting, and lifecycle hooks).

## Uninstall

```bash
helm uninstall transfergw -n transfergw
```

This does not delete any `TransferGW` resources or the `Gateway`/`HTTPRoute` objects
they generated — remove those first if you want a clean teardown:

```bash
kubectl delete transfergw --all -A
```

## Links

- [Source & full documentation](https://github.com/thev1ndu/transfergw)
- [Issues](https://github.com/thev1ndu/transfergw/issues)

## License

Apache License 2.0

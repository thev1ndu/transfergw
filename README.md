# TransferGW

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/transfergw)](https://artifacthub.io/packages/search?repo=transfergw)
[![Docker](https://img.shields.io/docker/v/thev1ndu/transfergw?logo=docker&label=docker)](https://hub.docker.com/r/thev1ndu/transfergw)

TransferGW is a Kubernetes operator that migrates `Ingress` resources to the
[Gateway API](https://gateway-api.sigs.k8s.io/) without a rip-and-replace
cutover. A single `TransferGW` custom resource selects a set of Ingresses by
namespace, label, or ingress class, converts them into `Gateway` and
`HTTPRoute` objects, and shifts traffic to the new stack at a controlled
percentage. Ingress annotations (nginx, cert-manager, and others as they're
added) are translated to their closest Gateway API equivalent, and anything
that can't be translated is reported as a status condition instead of being
silently dropped.

The goal is to make Ingress-to-Gateway migration something you declare and
watch, not something you script by hand across dozens of clusters.

## Getting started

### Install

```bash
helm repo add transfergw https://thev1ndu.github.io/transfergw
helm repo update

helm install transfergw transfergw/transfergw \
  --namespace transfergw --create-namespace

kubectl rollout status deployment/transfergw-controller -n transfergw
```

Or pull directly from the OCI registry instead of adding a classic repo:

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --version 1.0.0 --namespace transfergw --create-namespace
```

### Run a migration

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

kubectl get transfergw my-migration -n my-namespace -w
kubectl get httproute -n my-namespace
```

See [docs/SETUP.md](docs/SETUP.md) for the full `spec` reference (rollout
strategies, health-based rollback, alerting, lifecycle hooks) and
[docs/TESTING.md](docs/TESTING.md) for a complete walkthrough on a local kind
cluster.

## Project status

TransferGW is under active development. The table below reflects what the
controller actually does today versus what's designed but not yet built.

**1. Ingress → Gateway API conversion**
- [x] 1.1 Ingress selection by namespace, label, and ingress class
- [x] 1.2 Ingress → Gateway/HTTPRoute conversion
- [x] 1.3 Orphaned route (and GRPCRoute) cleanup
- [x] 1.4 Named Service backend ports resolved via a live lookup, instead of requiring a port number on the Ingress
- [x] 1.5 `backend-protocol: GRPC` (nginx, AGIC) generates a `GRPCRoute` instead of an `HTTPRoute`
- [x] 1.6 nginx weight-based canary Ingress pairs merged into one `HTTPRoute` with two weighted `backendRefs`
- [x] 1.7 Overlapping host+path across selected Ingresses (different backends, same match) reported as a warning

**2. Annotation translation**
- [x] 2.1 Full ingress-nginx annotation coverage (real filters where Gateway API has one, explicit guidance where it doesn't)
- [x] 2.2 cert-manager annotation translation
- [x] 2.3 Azure Application Gateway Ingress Controller (AGIC) annotation coverage
- [x] 2.4 Rule-level translation (session affinity → `sessionPersistence`, timeout annotations → `timeouts`) alongside filter-level translation, so a translated annotation doesn't have to look like the original mechanism — it has to produce the same effect
- [x] 2.5 Multi-annotation translation (CORS: `enable-cors` + its `cors-*` siblings combine into one core `HTTPRouteFilterCORS`, instead of each annotation only ever seeing itself in isolation)

**3. Rollout & safety**
- [x] 3.1 Percentage-based traffic rollout (immediate, gradual, canary)
- [x] 3.2 Health-based automatic rollback (Prometheus-backed threshold breach detection)
- [x] 3.3 Webhook alerting on rollback (per-migration via `spec.monitoring.alerting`, or a chart-wide default)
- [x] 3.4 Lifecycle hooks (`spec.lifecycle.hooks`: PreConversion/PreRollout gate progress on a 2xx response; PostConversion/PostRollout notify without blocking)
- [x] 3.5 Validating webhook (off by default — rejects an unknown `gatewayClass`, a `gatewayName` already owned by another `TransferGW`, or warns on an empty selector, before the first reconcile)

**4. Planned**
- [ ] 4.1 Multi-gateway support (Istio, Kong, cloud LBs)
- [ ] 4.2 Multi-cluster migrations
- [ ] 4.3 Slack alerting integration
- [ ] 4.4 ALB annotation coverage (`alb.ingress.kubernetes.io/*`)

## License

Apache License 2.0 — see [LICENSE](LICENSE).

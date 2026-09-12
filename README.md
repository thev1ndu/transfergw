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

See [docs/SETUP.md](docs/SETUP.md) for installation and
[docs/TESTING.md](docs/TESTING.md) for a full walkthrough on a
local kind cluster.

## Project status

TransferGW is under active development. The table below reflects what the
controller actually does today versus what's designed but not yet built.

**1. Ingress → Gateway API conversion**
- [x] 1.1 Ingress selection by namespace, label, and ingress class
- [x] 1.2 Ingress → Gateway/HTTPRoute conversion
- [x] 1.3 Orphaned route cleanup

**2. Annotation translation**
- [x] 2.1 Full ingress-nginx annotation coverage (real filters where Gateway API has one, explicit guidance where it doesn't)
- [x] 2.2 cert-manager annotation translation

**3. Rollout & safety**
- [x] 3.1 Percentage-based traffic rollout (immediate, gradual, canary)
- [x] 3.2 Health-based automatic rollback (Prometheus-backed threshold breach detection)
- [x] 3.3 Webhook alerting on rollback (per-migration via `spec.monitoring.alerting`, or a chart-wide default)
- [x] 3.4 Lifecycle hooks (`spec.lifecycle.hooks`: PreConversion/PreRollout gate progress on a 2xx response; PostConversion/PostRollout notify without blocking)

**4. Planned**
- [ ] 4.1 Multi-gateway support (Istio, Kong, cloud LBs)
- [ ] 4.2 Multi-cluster migrations
- [ ] 4.3 Slack alerting integration

## License

Apache License 2.0 — see [LICENSE](LICENSE).

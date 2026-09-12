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

| Capability | Status |
|---|---|
| Ingress selection (namespace, label, ingress class) | ✅ Available |
| Ingress → Gateway/HTTPRoute conversion | ✅ Available |
| nginx & cert-manager annotation translation | ✅ Available |
| Percentage-based traffic rollout | ✅ Available |
| Orphaned route cleanup | ✅ Available |
| Health-based automatic rollback | 🚧 Planned |
| Lifecycle hooks (pre/post conversion webhooks) | 🚧 Planned |
| Multi-gateway support (Istio, Kong, cloud LBs) | 🚧 Planned |
| Multi-cluster migrations | 🚧 Planned |
| Alerting integrations (Slack, webhooks) | 🚧 Planned |

## License

Apache License 2.0 — see [LICENSE](LICENSE).

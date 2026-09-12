# Live Migration Test: nginx → Envoy Gateway on the shared `main` Gateway

This runbook migrates one Ingress to the cluster's existing, shared Envoy Gateway
(`main` in `envoy-gateway-system`, the only Gateway with a public IP) and proves it
end-to-end over the real domain `testing.2take1.nl`, which already has a wildcard DNS
record pointing at that Gateway's address.

**`ingress-nginx` and `envoy-gateway` are pre-existing installs on this cluster and are
never touched, modified, or uninstalled by this runbook.** Everything here is additive:
a new namespace, a new sample app, one new Ingress, the TransferGW operator, and one
`TransferGW` resource.

## Why this needed a code change first

TransferGW always created a *new* Gateway named `<migration-name>-gateway`. On this
cluster that would sit at `<pending>` forever — there's no spare LoadBalancer IP, and
DNS is already pointed at the *existing* `main` Gateway's address (`169.58.222.25`), not
a new one. `testing.2take1.nl` would never reach it.

Fix: `spec.conversion.gatewayName` (new, optional) lets a `TransferGW` attach its
generated `HTTPRoute` to an existing Gateway instead of creating its own. Combined with
`generateGateway: false`, TransferGW never creates or touches the `main` Gateway — it
only creates an `HTTPRoute` that references it.

`main`'s listeners already allow this with no further changes:

```yaml
listeners:
  - name: http
    port: 80
    hostname: '*.2take1.nl'
    allowedRoutes: { namespaces: { from: All } }
  - name: https
    port: 443
    hostname: '*.2take1.nl'
    tls: { mode: Terminate, certificateRefs: [{ name: wildcard-tls }] }
    allowedRoutes: { namespaces: { from: All } }
```

`from: All` means any namespace's `HTTPRoute` may attach — no `ReferenceGrant` is needed
for the cross-namespace `HTTPRoute` → Gateway reference (that direction is authorized by
the listener's `allowedRoutes`, not by a grant; grants are only for a route's *backendRef*
crossing namespaces, which doesn't happen here).

## Preconditions (already confirmed, read-only)

| Check | Result |
|---|---|
| `kubectl get gatewayclass` | `envoy-gateway` — Accepted |
| `kubectl get ingressclass` | `nginx` |
| `main` Gateway address | `169.58.222.25`, Programmed: True |
| `main` Gateway hostname | `*.2take1.nl`, all namespaces allowed |
| `ingress-nginx-controller` Service | `LoadBalancer`, external IP `<pending>` (no public reachability — expected, baseline is checked in-cluster) |
| `transfergw` namespace/CRD | not present — the operator is not yet installed |

## Plan

1. Create `demo` namespace; deploy a sample nginx `Deployment`+`Service`.
2. Create an `Ingress` in `demo` for `testing.2take1.nl` (class `nginx`, label
   `migrate: "true"`).
3. Confirm the Ingress baseline works — **in-cluster**, since `ingress-nginx` has no
   public IP on this cluster. This is the "before" state and must keep returning 200
   for the rest of the test.
4. Install the TransferGW operator (Helm, namespace `transfergw`) — not yet present.
5. Apply a `TransferGW` selecting `demo`, `gatewayName: main`, `generateGateway: false`,
   `targetNamespace: envoy-gateway-system`, `rollout.mode: immediate`.
6. Confirm the generated `HTTPRoute` attaches to `main` and is `Accepted`.
7. Hit `http://testing.2take1.nl` for real, over the public internet, and confirm it
   serves the nginx sample page — through Envoy Gateway, proving the migration.

## Cleanup (not run automatically — your call after you're done testing)

```bash
kubectl delete transfergw demo-migration -n demo
helm uninstall transfergw -n transfergw
kubectl delete namespace transfergw
kubectl delete namespace demo
```

This removes only what this runbook created. `ingress-nginx` and `envoy-gateway` (and
the `main` Gateway) are left exactly as they were.

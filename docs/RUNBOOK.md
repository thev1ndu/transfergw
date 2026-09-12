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

## Steps (run in order; every command below was run against the live cluster)

### 1. `demo` namespace + sample nginx app

```bash
kubectl create namespace demo

kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-nginx
  namespace: demo
spec:
  replicas: 1
  selector:
    matchLabels: {app: sample-nginx}
  template:
    metadata:
      labels: {app: sample-nginx}
    spec:
      containers:
        - name: nginx
          image: nginxdemos/hello:plain-text
          ports: [{containerPort: 80}]
---
apiVersion: v1
kind: Service
metadata:
  name: sample-nginx
  namespace: demo
spec:
  selector: {app: sample-nginx}
  ports: [{port: 80, targetPort: 80}]
EOF

kubectl rollout status deployment/sample-nginx -n demo --timeout=60s
```

### 2. Ingress for `testing.2take1.nl`

```bash
kubectl apply -f - <<'EOF'
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: sample-nginx
  namespace: demo
  labels: {migrate: "true"}
spec:
  ingressClassName: nginx
  rules:
    - host: testing.2take1.nl
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: {name: sample-nginx, port: {number: 80}}
EOF
```

### 3. Baseline check (in-cluster — `ingress-nginx` has no public IP here)

```bash
kubectl run curl-ingress --rm -i --restart=Never -n demo \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -o /dev/null -w 'ingress: %{http_code}\n' \
  -H 'Host: testing.2take1.nl' http://ingress-nginx-controller.ingress-nginx.svc.cluster.local
```

Result: `ingress: 200`. This is the "before" state.

### 4. Install the TransferGW operator

`spec.conversion.gatewayName` only exists from commit `6716540` onward, so the image tag
is pinned explicitly rather than trusting `latest`:

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --version 1.0.0 --namespace transfergw --create-namespace \
  --set image.tag=sha-6716540

kubectl rollout status deployment/transfergw-controller -n transfergw --timeout=90s
```

### 5. Preview before applying (optional — makes no cluster changes)

Write the `TransferGW` to a file first instead of piping straight into `kubectl apply`,
and preview what it would generate:

```bash
cat > /tmp/demo-migration.yaml <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: demo-migration
  namespace: demo
spec:
  selector:
    namespaces: [demo]
    ingressSelector: {matchLabels: {migrate: "true"}}
    ingressClasses: [nginx]
  conversion:
    gatewayClass: envoy-gateway
    gatewayName: main
    targetNamespace: envoy-gateway-system
    generateGateway: false
  rollout:
    mode: immediate
EOF

go run ./cmd/preview -f /tmp/demo-migration.yaml
```

This runs the same selection and conversion logic the controller uses, against the
cluster's real Ingresses, and prints the `HTTPRoute` it would generate — no `TransferGW`,
`Gateway`, or `HTTPRoute` is created. `kubectl apply --dry-run` cannot do this (see
[`docs/TESTING.md`](TESTING.md#preview-a-migration-before-applying-it) for why); this is
the actual dry-run for TransferGW.

Catches exactly the kind of mistake that would otherwise only surface after applying:
`cmd/preview` initially printed `parentRefs: [{name: demo-migration-gateway}]` instead of
`main` here — it predated the `gatewayName` override and still hardcoded the hardcoded
`<name>-gateway` default. Fixed in `cmd/preview/main.go` to read
`spec.conversion.gatewayName` the same way the controller does. Confirmed output after
the fix:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sample-nginx
  namespace: demo
spec:
  hostnames:
  - testing.2take1.nl
  parentRefs:
  - name: main
    namespace: envoy-gateway-system
  rules:
  - backendRefs:
    - name: sample-nginx
      port: 80
    matches:
    - path:
        type: PathPrefix
        value: /
```

### 6. Apply the migration

```bash
kubectl apply -f /tmp/demo-migration.yaml
```

### 7. Confirm the generated HTTPRoute attached to `main`

```bash
kubectl get transfergw demo-migration -n demo -o wide
kubectl describe transfergw demo-migration -n demo
kubectl get httproute -n demo -o yaml
```

Result: phase `Complete`, `100%` on the Gateway; the `HTTPRoute`'s `parentRefs` points at
`main`/`envoy-gateway-system`, with `Accepted: True` and `ResolvedRefs: True`.

### 8. Real public verification

```bash
curl -s -o /dev/null -w 'testing.2take1.nl: %{http_code}\n' http://testing.2take1.nl
curl -s http://testing.2take1.nl | head -20
```

Result: `testing.2take1.nl: 200`, served by the `sample-nginx` pod — confirmed over the
public internet, through the existing `main` Envoy Gateway.

## Cleanup (not run automatically — your call after you're done testing)

```bash
kubectl delete transfergw demo-migration -n demo
helm uninstall transfergw -n transfergw
kubectl delete namespace transfergw
kubectl delete namespace demo
```

This removes only what this runbook created. `ingress-nginx` and `envoy-gateway` (and
the `main` Gateway) are left exactly as they were.

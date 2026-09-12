# TransferGW Runbook: Full User Guide

This is the complete, end-to-end guide to running TransferGW: install, your
first migration, canary and gradual rollouts, the declarative approval gate,
the validating webhook, lifecycle hooks, health-based rollback, and
day-to-day operations.

Every command and output in sections 1–5 was run against a real cluster in
one continuous session (not a `kind` sandbox — a persistent, multi-tenant
cluster already running ArgoCD, cert-manager, Cilium, and Kyverno), using
only two namespaces: `demo` (the workloads being migrated) and `transfergw`
(the controller and its generated resources). Nothing outside those two
namespaces was created, except the cert-manager `Issuer`/`Certificate` in
section 5, which also lives in `transfergw` and is cleaned up at the end of
that section.

Sections further down (lifecycle hooks, alerting, health rollback) are
documented accurately from the code and CRD schema but weren't re-run live
in this pass — each says so plainly rather than implying otherwise. A live
health-rollback walkthrough already exists in
[docs/TESTING.md](TESTING.md#things-worth-testing-deliberately).

## Contents

1. [Install](#1-install)
2. [Your first migration: a sample Ingress](#2-your-first-migration-a-sample-ingress)
3. [Canary rollout (nginx canary annotations)](#3-canary-rollout-nginx-canary-annotations)
4. [Gradual rollout + declarative approval](#4-gradual-rollout--declarative-approval)
5. [The validating webhook](#5-the-validating-webhook)
6. [Lifecycle hooks](#6-lifecycle-hooks)
7. [Health-based rollback and alerting](#7-health-based-rollback-and-alerting)
8. [Annotation coverage](#8-annotation-coverage)
9. [Day-to-day operations](#9-day-to-day-operations)
10. [Troubleshooting](#10-troubleshooting)
11. [Uninstall](#11-uninstall)
12. [Known gaps and no-op fields](#12-known-gaps-and-no-op-fields)

## 1. Install

### Prerequisites

- A Kubernetes cluster with the [Gateway API](https://gateway-api.sigs.k8s.io/) CRDs installed.
- A Gateway controller installed and its `GatewayClass` `Accepted` (Envoy
  Gateway is used throughout this guide; any Gateway API implementation
  works the same way from TransferGW's side).
- Helm 3.

### Install the controller — no tag needed

The chart's own default (`chart/transfergw/values.yaml`) already tracks
`latest` with `pullPolicy: IfNotPresent` — you never need to pass
`--set image.tag=...` for a normal install:

```bash
kubectl create namespace demo

helm install transfergw ./chart/transfergw \
  --namespace transfergw --create-namespace \
  --set image.pullPolicy=Always
```

**Why `pullPolicy=Always` here, overriding the chart default**: found live,
on the cluster this guide was written against — a node that had ever pulled
`thev1ndu/transfergw:latest` before (even from an old, unrelated session)
keeps serving that same cached image under `IfNotPresent`, silently,
forever, no matter how many times the tag is re-pushed upstream. This isn't
a TransferGW bug, it's how every `:latest` + `IfNotPresent` combination
behaves on every Kubernetes node — but it's exactly the kind of thing that
produces a confusing "the fix I just shipped isn't working" moment, so it's
called out here plainly. `Always` costs one extra registry check per pod
start and removes the risk entirely. If you're installing on a node that
has genuinely never pulled this image before, `IfNotPresent` alone is fine
too — the risk is specifically re-pulls of a tag you've already cached.

`enableWebhooks` and `webhookCABundle` are deliberately left at chart
defaults here (`true` / empty) for now — see
[§5](#5-the-validating-webhook) for why that combination needs one more
step before the controller will actually start cleanly, and do that step
before installing if you want the webhook on immediately. Sections 2–4
below install with `enableWebhooks=false` to keep the walkthrough focused.

```bash
helm install transfergw ./chart/transfergw \
  --namespace transfergw --create-namespace \
  --set image.pullPolicy=Always \
  --set enableWebhooks=false
```

### Verify

```bash
$ kubectl get pods -n transfergw -o wide
NAME                                     READY   STATUS    RESTARTS   AGE
transfergw-controller-59d98587fb-hbxd6   1/1     Running   0          18s
transfergw-controller-59d98587fb-j7rmj   1/1     Running   0          18s

$ kubectl get crd transfergws.transfergw.t-1.dev
NAME                             SCOPE        VERSIONS
transfergws.transfergw.t-1.dev   Namespaced   v1beta1(storage)
```

Two replicas by default (`chart/transfergw/values.yaml`'s
`replicaCount: 2`), one leader elected via the standard controller-runtime
lease mechanism — only the leader reconciles, both serve metrics/health.

## 2. Your first migration: a sample Ingress

Deploy a workload and an `Ingress` — this is the thing being migrated, not
part of TransferGW itself:

```bash
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
  namespace: demo
  labels: {app: sample-app, app.kubernetes.io/name: sample-app}
spec:
  replicas: 1
  selector: {matchLabels: {app: sample-app}}
  template:
    metadata: {labels: {app: sample-app, app.kubernetes.io/name: sample-app}}
    spec:
      containers:
        - name: web
          image: nginx:1.27-alpine
          ports: [{containerPort: 80}]
          resources:
            requests: {cpu: 50m, memory: 32Mi}
            limits: {cpu: 200m, memory: 64Mi}
          readinessProbe: {httpGet: {path: /, port: 80}, initialDelaySeconds: 2}
          livenessProbe: {httpGet: {path: /, port: 80}, initialDelaySeconds: 5}
---
apiVersion: v1
kind: Service
metadata:
  name: sample-app
  namespace: demo
spec:
  selector: {app: sample-app}
  ports: [{port: 80, targetPort: 80}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: sample-app
  namespace: demo
  labels:
    migrate: "true"           # <- this is what a TransferGW's ingressSelector matches on
    app.kubernetes.io/name: sample-app
spec:
  ingressClassName: nginx
  rules:
    - host: sample-app.cncf-demo.local
      http:
        paths:
          - path: /
            pathType: Prefix
            backend: {service: {name: sample-app, port: {number: 80}}}
EOF
```

Nothing about writing this `Ingress` is TransferGW-specific — it's a
completely ordinary Ingress. The only thing that matters for migration is
the `migrate: "true"` label, and only because that's the label this guide's
`TransferGW` objects choose to select on (any label works; you name it in
`spec.selector.ingressSelector`).

### Apply the migration

```bash
kubectl apply -f - <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: sample-app-migration
  namespace: transfergw
spec:
  selector:
    namespaces: [demo]
    ingressSelector: {matchLabels: {migrate: "true"}}
  conversion:
    gatewayClass: envoy-gateway
    generateGateway: true
    targetNamespace: transfergw
  rollout:
    mode: immediate
EOF
```

```bash
$ kubectl get transfergw -n transfergw sample-app-migration
NAME                    PHASE      PROGRESS   INGRESS   GATEWAY   AGE
sample-app-migration    Complete   100                  100       6s

$ kubectl get gateway,httproute -n transfergw -A
```

`phase: Complete`, `completionPercentage: 100` — one Ingress converted, a
`Gateway` (`sample-app-migration-gateway`, in `transfergw` since
`targetNamespace: transfergw`) and an `HTTPRoute` (in `demo`, alongside its
source Ingress — generated routes always live next to the Ingress they
came from, not in the Gateway's namespace) both created. The original
`Ingress` is untouched: TransferGW never edits or deletes it.

```bash
kubectl delete transfergw sample-app-migration -n transfergw
```

(cleanup before the next section — the sample app itself is reused there)

## 3. Canary rollout (nginx canary annotations)

This is the case where you already have two versions of a service running
behind a weighted nginx canary pair, and want that same weighted split to
survive the move to Gateway API — not become two independent routes.

```bash
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: checkout-v1, namespace: demo, labels: {app: checkout-v1}}
spec:
  replicas: 1
  selector: {matchLabels: {app: checkout-v1}}
  template:
    metadata: {labels: {app: checkout-v1}}
    spec:
      containers:
        - name: web
          image: nginx:1.27-alpine
          ports: [{containerPort: 80}]
          resources: {requests: {cpu: 50m, memory: 32Mi}, limits: {cpu: 200m, memory: 64Mi}}
          readinessProbe: {httpGet: {path: /, port: 80}, initialDelaySeconds: 2}
          livenessProbe: {httpGet: {path: /, port: 80}, initialDelaySeconds: 5}
---
apiVersion: v1
kind: Service
metadata: {name: checkout-v1, namespace: demo}
spec: {selector: {app: checkout-v1}, ports: [{port: 80, targetPort: 80}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: checkout-v2, namespace: demo, labels: {app: checkout-v2}}
spec:
  replicas: 1
  selector: {matchLabels: {app: checkout-v2}}
  template:
    metadata: {labels: {app: checkout-v2}}
    spec:
      containers:
        - name: web
          image: nginxdemos/hello:plain-text
          ports: [{containerPort: 80}]
          resources: {requests: {cpu: 50m, memory: 32Mi}, limits: {cpu: 200m, memory: 64Mi}}
          readinessProbe: {httpGet: {path: /, port: 80}, initialDelaySeconds: 2}
          livenessProbe: {httpGet: {path: /, port: 80}, initialDelaySeconds: 5}
---
apiVersion: v1
kind: Service
metadata: {name: checkout-v2, namespace: demo}
spec: {selector: {app: checkout-v2}, ports: [{port: 80, targetPort: 80}]}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: checkout-primary
  namespace: demo
  labels: {migrate: "true"}
spec:
  ingressClassName: nginx
  rules:
    - host: checkout.cncf-demo.local
      http:
        paths:
          - path: /
            pathType: Prefix
            backend: {service: {name: checkout-v1, port: {number: 80}}}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: checkout-canary
  namespace: demo
  labels: {migrate: "true"}
  annotations:
    nginx.ingress.kubernetes.io/canary: "true"
    nginx.ingress.kubernetes.io/canary-weight: "20"
spec:
  ingressClassName: nginx
  rules:
    - host: checkout.cncf-demo.local
      http:
        paths:
          - path: /
            pathType: Prefix
            backend: {service: {name: checkout-v2, port: {number: 80}}}
EOF
```

Two Ingresses, same host and path, one marked as nginx's canary mechanism
at 20%. Migrate both:

```bash
kubectl apply -f - <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: canary-demo
  namespace: transfergw
spec:
  selector:
    namespaces: [demo]
    ingressSelector: {matchLabels: {migrate: "true"}}
  conversion:
    gatewayClass: envoy-gateway
    generateGateway: true
    targetNamespace: transfergw
  rollout:
    mode: immediate
EOF
```

```bash
$ kubectl get httproute -n demo
NAME               HOSTNAMES                      AGE
checkout-primary   ["checkout.cncf-demo.local"]   7s
```

Only **one** `HTTPRoute` — `checkout-canary` isn't independently converted,
it's folded into `checkout-primary`'s route as a second weighted
`backendRef`:

```bash
$ kubectl get httproute -n demo checkout-primary -o yaml
spec:
  hostnames: [checkout.cncf-demo.local]
  rules:
  - backendRefs:
    - {group: "", kind: Service, name: checkout-v1, port: 80, weight: 80}
    - {group: "", kind: Service, name: checkout-v2, port: 80, weight: 20}
    matches: [{path: {type: PathPrefix, value: /}}]
```

`weight: 80` / `weight: 20` — exactly the nginx `canary-weight: "20"`
this started from. Proved with real traffic, not just the object's spec
(`/` served by `nginx` on v1, and by `nginxdemos/hello`, which prints
`Server address`, on v2):

```bash
$ for i in $(seq 1 20); do
    curl -s -H "Host: checkout.cncf-demo.local" http://<gateway-address>:<nodeport>/ \
      | grep -o "Welcome to nginx\|Server address"
  done | sort | uniq -c
   4 Server address
  16 Welcome to nginx
```

16/20 = 80%, 4/20 = 20% — the real split, not an assumption.

`status.issues` still records the `canary`/`canary-weight` annotations as
"no core Gateway API equivalent" on the canary Ingress's own entry — that's
expected and correct: the annotation itself has no direct HTTPRoute filter
equivalent, but its *effect* (the weighted split) is fully reproduced
through the merge, and a separate info-level issue on the primary records
exactly that: `merged canary Ingress demo/checkout-canary as a 20% weighted
backend`.

Cleanup:

```bash
kubectl delete transfergw canary-demo -n transfergw
kubectl delete deploy,svc checkout-v1 checkout-v2 -n demo
kubectl delete ingress checkout-primary checkout-canary -n demo
```

## 4. Gradual rollout + declarative approval

This is the newest feature (README 3.6): a `terraform plan` / `apply`
split, done entirely through the `TransferGW` CR — no separate CLI. The
rollout schedule computes its next step every reconcile; with
`requireApproval: true`, that step is held until a human bumps
`approvedPercentage` to match it.

Redeploy the sample app from §2 first if you cleaned it up, then:

```bash
kubectl apply -f - <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: gradual-demo
  namespace: transfergw
spec:
  selector:
    namespaces: [demo]
    ingressSelector: {matchLabels: {migrate: "true"}}
  conversion:
    gatewayClass: envoy-gateway
    generateGateway: true
    targetNamespace: transfergw
  rollout:
    mode: gradual
    canary:
      initialPercentage: 20
      increment: 30
      stepDuration: 20s
    requireApproval: true
    approvedPercentage: 0
EOF
```

`stepDuration: 20s` here only to make the schedule advance fast enough to
watch in a terminal — a real rollout would use something like `1h` or `6h`.

```bash
$ kubectl get transfergw -n transfergw gradual-demo \
    -o jsonpath='{"phase: "}{.status.phase}{"\ncompletion: "}{.status.completionPercentage}{"\npendingPlan: "}{.status.pendingPlan}'
phase: Canary
completion:
pendingPlan: {"nextPercentage":20}
```

`completionPercentage` is empty (held at 0); `pendingPlan.nextPercentage`
already shows what the schedule wants (`20`, the configured
`initialPercentage`) — computed and published, not yet applied.

Approve it:

```bash
$ kubectl patch transfergw -n transfergw gradual-demo --type=merge \
    -p '{"spec":{"rollout":{"approvedPercentage":20}}}'

$ kubectl get transfergw -n transfergw gradual-demo \
    -o jsonpath='{"completion: "}{.status.completionPercentage}{"\npendingPlan: "}{.status.pendingPlan}'
completion: 20
pendingPlan: {"currentPercentage":20,"nextPercentage":20}
```

`completionPercentage` moved the moment `approvedPercentage` matched — no
CLI, just a field patch.

Wait past `stepDuration` (here, 20s) and the schedule advances on its own,
regardless of approval — approval only holds the traffic percentage back,
it never blocks the schedule from computing what it *wants* next:

```bash
# ~22s later
$ kubectl get transfergw -n transfergw gradual-demo \
    -o jsonpath='{"completion: "}{.status.completionPercentage}{"\npendingPlan: "}{.status.pendingPlan}'
completion: 20
pendingPlan: {"currentPercentage":20,"nextPercentage":80}
```

`nextPercentage` jumped to 80 (`20` initial + two elapsed 30% increments,
timed from the `TransferGW`'s own creation, not from when it was
approved); `completionPercentage` stayed at 20 — the gate held, exactly as
designed. Approve up to where the schedule already is, and it catches up
immediately:

```bash
$ kubectl patch transfergw -n transfergw gradual-demo --type=merge \
    -p '{"spec":{"rollout":{"approvedPercentage":80}}}'
# completion: 80, pendingPlan: {"currentPercentage":80,"nextPercentage":100}

$ kubectl patch transfergw -n transfergw gradual-demo --type=merge \
    -p '{"spec":{"rollout":{"approvedPercentage":100}}}'
# (after the schedule itself also reaches 100)
$ kubectl get transfergw -n transfergw gradual-demo \
    -o jsonpath='{"phase: "}{.status.phase}{"\ncompletion: "}{.status.completionPercentage}'
phase: Complete
completion: 100
```

Full path proven live: **0 (held) → 20 → 80 → 100 (Complete)**.

One more thing worth knowing, proven separately (see
[REPORT.md](../REPORT.md)): the underlying `HTTPRoute`/`Gateway` are kept in
sync with the selected Ingresses on every reconcile *regardless* of
approval state — only the traffic percentage a DNS/LB layer would read is
gated. A route existing doesn't move traffic by itself (see the
`rolloutPercentage` comment in
`internal/controller/transfergw_controller.go`), so there's nothing unsafe
about that route already being correct while the percentage is still held
at 0.

Full details, including the exact `status.pendingPlan`/`routeChanges` shape
and a worked example: [docs/DEMO.md](DEMO.md).

Cleanup: `kubectl delete transfergw gradual-demo -n transfergw`

## 5. The validating webhook

The chart ships a validating webhook (`internal/controller/webhook`) that
rejects a `TransferGW` at admission time for three specific mistakes,
instead of letting them surface only after the first reconcile:

| Check | Outcome |
|---|---|
| `spec.conversion.gatewayClass` doesn't exist in the cluster | **Rejected** |
| `spec.conversion.gatewayName` is already owned by another `TransferGW` (both with `generateGateway: true`) | **Rejected** |
| `spec.selector` matches no Ingress yet | **Warning only, still allowed** — the selector might match something created moments later |

**It needs a real TLS certificate to actually run** — the chart does not
provision one for you (`chart/transfergw/values.yaml`'s own comment says
so), and turning `enableWebhooks: true` on without one makes the controller
pod crash on startup (`open .../serving-certs/tls.crt: no such file or
directory` — found live doing exactly this while writing this section).
This is expected, documented behavior, not a bug: the flag's own `--help`
text says as much.

cert-manager is already installed on most clusters running other
CNCF-ecosystem tooling (it was on this one) — wire a self-signed cert for
just this webhook, entirely inside the `transfergw` namespace:

```bash
kubectl apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: transfergw-webhook-selfsigned
  namespace: transfergw
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: transfergw-webhook-cert
  namespace: transfergw
spec:
  secretName: transfergw-webhook-certs
  dnsNames:
    - transfergw-webhook-service.transfergw.svc
    - transfergw-webhook-service.transfergw.svc.cluster.local
  issuerRef: {name: transfergw-webhook-selfsigned, kind: Issuer}
EOF

kubectl wait --for=condition=Ready certificate/transfergw-webhook-cert -n transfergw --timeout=60s
```

Turn the webhook on with that cert's CA bundle:

```bash
CA_BUNDLE=$(kubectl get secret transfergw-webhook-certs -n transfergw -o jsonpath='{.data.ca\.crt}')

helm upgrade transfergw ./chart/transfergw -n transfergw \
  --reuse-values \
  --set enableWebhooks=true \
  --set webhookCABundle="$CA_BUNDLE"

kubectl rollout restart deployment/transfergw-controller -n transfergw
kubectl rollout status deployment/transfergw-controller -n transfergw --timeout=90s
```

Both pods come up clean (`1/1 Running`, no crash) — the cert exists now.

### Proving all three rules, for real

```bash
$ kubectl apply -f - <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata: {name: webhook-test-bad-class, namespace: transfergw}
spec:
  selector: {namespaces: [demo], ingressSelector: {matchLabels: {migrate: "true"}}}
  conversion: {gatewayClass: does-not-exist, generateGateway: true}
  rollout: {mode: immediate}
EOF
Error from server (Forbidden): admission webhook "vtransfergw.kb.io" denied the request:
spec.conversion.gatewayClass "does-not-exist" does not exist in this cluster
```

```bash
$ kubectl apply -f - <<'EOF'   # first one - succeeds, claims shared-gw
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata: {name: webhook-test-owner-a, namespace: transfergw}
spec:
  selector: {namespaces: [demo], ingressSelector: {matchLabels: {migrate: "true"}}}
  conversion: {gatewayClass: envoy-gateway, generateGateway: true, gatewayName: shared-gw}
  rollout: {mode: immediate}
EOF
transfergw.transfergw.t-1.dev/webhook-test-owner-a created

$ kubectl apply -f - <<'EOF'   # second one, same gatewayName - rejected
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata: {name: webhook-test-owner-b, namespace: transfergw}
spec:
  selector: {namespaces: [demo], ingressSelector: {matchLabels: {migrate: "true"}}}
  conversion: {gatewayClass: envoy-gateway, generateGateway: true, gatewayName: shared-gw}
  rollout: {mode: immediate}
EOF
Error from server (Forbidden): admission webhook "vtransfergw.kb.io" denied the request:
gateway transfergw/shared-gw is already owned by TransferGW transfergw/webhook-test-owner-a
```

```bash
$ kubectl apply -f - <<'EOF'   # selector matches nothing - warns, still created
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata: {name: webhook-test-empty-selector, namespace: transfergw}
spec:
  selector: {namespaces: [does-not-exist-ns]}
  conversion: {gatewayClass: envoy-gateway, generateGateway: true}
  rollout: {mode: immediate}
EOF
Warning: spec.selector matches no Ingress resources yet
transfergw.transfergw.t-1.dev/webhook-test-empty-selector created
```

All three, exactly as documented in the code's own comments.

Cleanup:

```bash
kubectl delete transfergw webhook-test-owner-a webhook-test-empty-selector -n transfergw
kubectl delete certificate transfergw-webhook-cert -n transfergw
kubectl delete issuer transfergw-webhook-selfsigned -n transfergw
```

(the Secret `transfergw-webhook-certs` is owned by the `Certificate` and is
deleted along with it; leave `enableWebhooks`/`webhookCABundle` as-is on the
release, or `helm upgrade ... --set enableWebhooks=false` to go back to the
simpler setup from earlier sections)

## 6. Lifecycle hooks

*(Documented from `api/v1beta1/transfergw_types.go` and
`internal/lifecycle/hook.go` — not re-run live in this pass; the mechanism
is otherwise identical to §7's alerting webhook, which was.)*

Four stages, each an HTTP `POST`:

```yaml
spec:
  lifecycle:
    hooks:
      preConversion: {url: "https://hooks.example.com/pre-conversion"}
      postConversion: {url: "https://hooks.example.com/post-conversion"}
      preRollout: {url: "https://hooks.example.com/pre-rollout"}
      postRollout: {url: "https://hooks.example.com/post-rollout"}
```

- **`preConversion`, `preRollout`** are *blocking*: the migration holds at
  `phase: Pending` until the endpoint responds 2xx. `preRollout` gates only
  the first move away from 0% — once approved, the rollout schedule runs
  on its own for every step after that (this is independent of, and
  compatible with, §4's `requireApproval`).
- **`postConversion`, `postRollout`** are *notify-only*: they never block,
  there's nothing left to hold back by that point.
- Each hook is called once per spec generation — editing the `TransferGW`
  re-triggers it; leaving it alone doesn't re-call it every reconcile.

Payload (`internal/lifecycle/hook.go`'s `Event`):

```json
{"migration": "transfergw/sample-app-migration", "stage": "PreRollout", "time": "2026-09-12T16:00:00Z"}
```

A hook that returns non-2xx (or times out) is treated as "not yet
approved" for a blocking stage; a missing `HookCaller` wiring (shouldn't
happen via the standard chart, which always wires one) fails open with a
one-time log line, same fail-open default as an unconfigured metrics
source.

## 7. Health-based rollback and alerting

Configuration (`spec.monitoring`):

```yaml
spec:
  monitoring:
    enabled: true
    interval: 5m
    thresholds:
      errorRate: 0.05          # 5% - compared Gateway vs Ingress
      latencyMs: 150
      latencyPercentile: p95
      connectionResets: 0.01
      throughputDelta: 0.2
    alerting:
      enabled: true
      webhookUrl: "https://alerts.example.com/webhook"
```

Needs a Prometheus reachable from the controller (`--prometheus-url` /
`TRANSFERGW_PROMETHEUS_URL`) with metrics from both the old Ingress
controller and the new Gateway implementation — health comparison is
silently skipped without one, same as leaving `monitoring` unset. A live
walkthrough of this, including watching an actual rollback fire, is in
[docs/TESTING.md](TESTING.md#things-worth-testing-deliberately).

On a threshold breach, the Gateway's traffic share is taken all the way
back to 0% (not to whatever the last known-good step was — see the
`rollbackPercentage` comment in the controller for why), `phase` becomes
`RolledBack`, and the migration holds there until the spec is edited (a
bump in `metadata.generation`) — it will not silently retry the same
percentage and roll back again in a loop.

Alerting (`spec.monitoring.alerting`) fires a webhook `POST` on that
rollback:

```json
{"migration": "transfergw/sample-app-migration", "phase": "RolledBack", "reason": "ThresholdBreached", "message": "Rolled back to 0%: ...", "time": "2026-09-12T16:00:00Z"}
```

A `TransferGW` that leaves `alerting` unset entirely falls back to the
chart's cluster-wide `alerting.webhookUrl` default
(`chart/transfergw/values.yaml`); one that sets it explicitly (including
`enabled: false`) always overrides that default. A delivery failure is
logged, not treated as a reconcile error — the rollback already happened
and is already recorded on `status`.

## 8. Annotation coverage

Ingress annotations from four vendors are translated to real Gateway API
filters where one exists, and to an explicit, actionable warning
(`status.issues`) where none does — never silently dropped:

- **ingress-nginx** (`nginx.ingress.kubernetes.io/*`)
- **cert-manager** (`cert-manager.io/*`)
- **Azure Application Gateway Ingress Controller** (`appgw.ingress.kubernetes.io/*`)
- **AWS Load Balancer Controller** (`alb.ingress.kubernetes.io/*`)

Full coverage list and the reasoning behind each translation choice:
[README.md § Project status](../README.md#project-status). To see exactly
what a given annotation resolves to without touching the cluster, use
`cmd/preview` (see [docs/TESTING.md](TESTING.md#preview-a-migration-before-applying-it)).

## 9. Day-to-day operations

```bash
# Pause a rollout in place (routes stay, percentage stops advancing)
kubectl patch transfergw <name> -n <ns> --type=merge -p '{"spec":{"rollout":{"paused":true}}}'

# Resume
kubectl patch transfergw <name> -n <ns> --type=merge -p '{"spec":{"rollout":{"paused":false}}}'

# Watch progress
kubectl get transfergw -n <ns> -w

# Full status, including conditions and any conversion issues
kubectl describe transfergw <name> -n <ns>

# Just the issues, as JSON
kubectl get transfergw <name> -n <ns> -o jsonpath='{.status.issues}' | jq

# Controller logs
kubectl logs -f -n transfergw deploy/transfergw-controller
```

There is no `kubectl`-level "rollback to Ingress" command and no supported
way to force `completionPercentage` by patching `status` directly (`status`
is server-managed — the controller overwrites it on the very next
reconcile). To go back to 0% deliberately: either lower
`spec.rollout.approvedPercentage` if `requireApproval` is set (§4), or edit
`spec.rollout.mode`/`canary` to a schedule that computes a lower step, or
delete the `TransferGW` outright — the original `Ingress` was never touched
and keeps serving through the old path the whole time.

## 10. Troubleshooting

**Controller crash-loops with `open .../serving-certs/tls.crt: no such file
or directory`** — `enableWebhooks: true` (the chart default) with no
`webhookCABundle`/cert provisioned. Either `--set enableWebhooks=false`, or
do the cert-manager wiring in [§5](#5-the-validating-webhook) first.

**A fix/update doesn't seem to have taken effect after `helm upgrade`** —
check the actual running image digest, not just the tag:

```bash
kubectl get pods -n transfergw -o jsonpath='{.items[0].status.containerStatuses[0].imageID}'
```

If you're tracking `:latest` (or any other mutable tag) and the node has
ever pulled that exact tag before, `imagePullPolicy: IfNotPresent` (the
chart default) will keep serving the old cached layer indefinitely. Use
`--set image.pullPolicy=Always`, or pin an immutable `sha-<commit>` tag if
you want a specific, reproducible build (both are published by this
project's CI on every push to `main`).

**Ingress not converting** — check the selector actually matches:

```bash
kubectl get ingress -A -L migrate   # or whatever label your selector uses
kubectl get transfergw <name> -n <ns> -o jsonpath='{.spec.selector}'
```

An empty match is a webhook *warning*, not an error (§5) — the
`TransferGW` still gets created and will pick up a matching Ingress the
moment one appears.

**A specific annotation "has no core Gateway API equivalent"** — expected
for anything without a portable filter; read the `recommendation` field
next to it in `status.issues`, it names the specific implementation-level
workaround.

## 11. Uninstall

```bash
kubectl delete transfergws --all --all-namespaces
helm uninstall transfergw -n transfergw
kubectl delete namespace transfergw
```

`helm uninstall` also removes the CRD (`chart/transfergw/templates/crd.yaml`
is a plain chart template, not exempted the way Helm's dedicated `crds/`
directory would be) — deleting it removes every `TransferGW` object's
history along with it, which is why the explicit `kubectl delete transfergws`
above runs first, so their finalizers/owned resources clean up normally
rather than racing the CRD's own removal.

## 12. Known gaps and no-op fields

Found reading the code while writing this guide — listed here so this
document doesn't quietly repeat them as if they worked:

- **`spec.rollout.gradual` has no effect.** `mode: gradual` and
  `mode: canary` both compute their percentage from `spec.rollout.canary`
  only (`internal/controller/transfergw_controller.go`'s
  `rolloutPercentage`/`canaryPercentage`) — `GradualSpec`'s
  `totalDuration`/`step` fields exist on the CRD but are never read.
  Section 4 above configures `spec.rollout.canary` for its `gradual`-mode
  example for exactly this reason.
- **`spec.monitoring.alerting.slackChannel` and `spec.monitoring.metrics`
  have no effect** — present on the CRD, never read anywhere in
  `internal/alert` or `internal/health`. Alerting always goes to
  `webhookUrl`; which metrics get compared is fixed (error rate, latency,
  connection resets, throughput), not configurable via `metrics`.
- **No CLI equivalent for a one-shot, no-controller-install migration** —
  everything here needs the controller running. README 4.4 tracks this as
  a possible future addition.

None of these block anything documented above — they're just fields on the
CRD that look configurable and currently aren't. Filed here instead of
silently working around them in the examples.

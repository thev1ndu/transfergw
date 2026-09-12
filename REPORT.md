# TransferGW — Live End-to-End Test Report

**Date:** 2026-09-12
**Purpose:** Prove TransferGW's Ingress → Gateway API migration, including the
newly-added declarative plan/diff feature (`spec.rollout.requireApproval`),
against a real Kubernetes cluster — not a fake client, not a disposable
`kind` cluster with nothing else running on it. Prepared as supporting
evidence for a CNCF submission.

Every command below was actually run against the cluster described in
[Environment](#environment), in the order shown, with its real output
pasted in (trimmed only for length, never edited for content). One real bug
was found and fixed mid-run — that's reported too, not hidden, because a
live end-to-end run that never finds anything is not doing its job.

## Constraints given for this run

1. Assume nginx (the legacy Ingress controller) has no public IP; only
   Envoy Gateway does. This models the realistic case a migration exists
   to solve: the old path has no stable external entrypoint, the new one
   does.
2. Use only two namespaces: `demo` (the workload being migrated) and
   `transfergw` (the controller and its generated Gateway).
3. Track every command, its output, and the reasoning behind it.

Constraint 1 turned out to already be true of this cluster's actual state,
not something staged — see [Pre-migration state](#2-pre-migration-state).

## Environment

A real, persistent, single-node cluster — not a throwaway `kind` cluster —
already running other workloads (ArgoCD, cert-manager, Cilium, Kyverno,
KEDA, KubeVela) prior to this test. Nothing was uninstalled or reconfigured
on the cluster to accommodate this run.

```
$ kubectl get nodes -o wide
NAME     STATUS   ROLES                  AGE   VERSION   INTERNAL-IP      EXTERNAL-IP
2take1   Ready    control-plane,worker   10d   v1.37.0   100.123.126.24   <none>

$ kubectl version
Client Version: v1.35.0
Server Version: v1.37.0
```

Pre-existing and left untouched:
- **ingress-nginx** — installed as the legacy Ingress controller (11h old
  at the start of this run).
- **Envoy Gateway** — installed and running (10d old), with one pre-existing
  `Gateway` named `main` in `envoy-gateway-system`, already `PROGRAMMED: True`
  with a real routable address.
- **Kyverno**, in `Audit` mode only on every policy (`require-labels`,
  `require-pod-probes`, `require-requests-limits`, `disallow-latest-tag`,
  `restrict-nodeport`, `restrict-external-ips`, `disallow-default-namespace`,
  `check-deprecated-apis`) — confirmed with
  `kubectl get clusterpolicy -o custom-columns=NAME:.metadata.name,ACTION:.spec.validationFailureAction`
  before writing any manifest, so audit-only policies wouldn't silently
  reject something mid-run. Manifests below still follow every one of these
  policies anyway (real probes, real resource limits, pinned image tags,
  no NodePort/externalIP Services) as a matter of practice, not because
  enforcement required it.
- **ArgoCD**, with exactly one managed `Application` (`kyverno`) —
  confirmed via `kubectl get applications -n argocd` before touching
  anything, so nothing in this run risked fighting a GitOps reconciler.
- **Cilium**, providing the cluster's CNI and its only LoadBalancer IP
  allocation mechanism (`CiliumLoadBalancerIPPool`), which matters directly
  — see [step 5](#5-a-genuine-limitation-the-new-gateway-has-no-public-ip-either).

**Controller image**: `thev1ndu/transfergw:sha-44d07b2`, then
`thev1ndu/transfergw:sha-34f7ffa` after the fix in step 4 — both built and
pushed by this repo's own `Build and Push Image` GitHub Actions workflow
directly from the commit under test, not built locally. Confirmed via:

```
$ gh run list --workflow=build.yaml --branch=main --limit=1
completed  success  ...  Build and Push Image  main  push  ...
```

## 1. Install the controller

```
$ kubectl create namespace demo
namespace/demo created

$ helm install transfergw ./chart/transfergw \
    --namespace transfergw --create-namespace \
    --set image.tag=sha-44d07b2 \
    --set enableWebhooks=false \
    --wait --timeout=180s
NAME: transfergw
LAST DEPLOYED: Sat Sep 12 21:23:51 2026
NAMESPACE: transfergw
STATUS: deployed
REVISION: 1
```

**Why `enableWebhooks=false`**: the chart's validating webhook needs a real
TLS certificate at `webhookCABundle` — the chart deliberately does not
provision one (see the comment in `chart/transfergw/values.yaml`), and the
binary's own `-enable-validating-webhook` flag documents that turning it on
without one makes every `TransferGW` apply fail closed. cert-manager is
running on this cluster and could wire this properly, but doing so wasn't
essential to proving the migration/plan-diff behavior this run exists to
verify, and would have added a `Certificate`/`Issuer` outside the two
allowed namespaces' natural scope. The webhook has its own passing unit
suite (`internal/controller/webhook`) — this run doesn't re-prove it live,
and says so rather than skipping it silently.

```
$ kubectl get pods -n transfergw -o wide
NAME                                     READY   STATUS    RESTARTS   AGE
transfergw-controller-6585ddf687-kt4bp   1/1     Running   0          32s
transfergw-controller-6585ddf687-r4fd2   1/1     Running   0          32s

$ kubectl logs -n transfergw deploy/transfergw-controller --tail=30
2026-09-12T15:54:18Z  INFO  setup  starting manager  {"version": "v1beta1"}
2026-09-12T15:54:18Z  INFO  leaderelection  Successfully acquired lease  {"lock": "transfergw/transfergw.t-1.dev"}
2026-09-12T15:54:18Z  INFO  Starting EventSource  {"controller": "transfergw", ..., "source": "kind source: *v1.HTTPRoute"}
2026-09-12T15:54:18Z  INFO  Starting EventSource  {"controller": "transfergw", ..., "source": "kind source: *v1.Ingress"}
2026-09-12T15:54:18Z  INFO  Starting EventSource  {"controller": "transfergw", ..., "source": "kind source: *v1beta1.TransferGW"}
2026-09-12T15:54:18Z  INFO  Starting Controller  {"controller": "transfergw", ...}
2026-09-12T15:54:18Z  INFO  Starting workers  {"controller": "transfergw", ..., "worker count": 1}
```

Both replicas up, leader elected, watching `TransferGW`, `Ingress`, and
`HTTPRoute` as expected, no errors.

## 2. Pre-migration state

Deployed a sample workload in `demo`: `nginx:1.27-alpine` behind a
`ClusterIP` Service and an `nginx`-class `Ingress`, labeled `migrate: "true"`
so the selector below picks it up.

(First attempt used `registry.k8s.io/e2e-test-images/agnhost:2.45` — the
same image this repo's own `test/e2e` suite uses — but this cluster's
egress hit a real network fault reaching that registry:
`read tcp [...]:58786->[...]:443: read: connection reset by peer` over
IPv6, confirmed via `kubectl describe pod`. Switched to `nginx:1.27-alpine`,
the image `docs/TESTING.md` already documents as known-good on a live
cluster, and it pulled and started immediately. Noted here because a
report that silently swaps images without saying why is exactly the kind
of gap this document is trying to avoid.)

```
$ kubectl get ingress -n demo sample-app -o wide
NAME         CLASS   HOSTS                        ADDRESS     PORTS   AGE
sample-app   nginx   sample-app.cncf-demo.local   localhost   80      17s

$ kubectl get svc -n ingress-nginx ingress-nginx-controller
NAME                       TYPE           CLUSTER-IP      EXTERNAL-IP   PORT(S)
ingress-nginx-controller   LoadBalancer   10.102.39.112   <pending>     80:32184/TCP,443:30249/TCP
```

This is constraint 1, confirmed rather than staged: the Ingress's
`ADDRESS` column shows `localhost` (ingress-nginx's own
`--publish-status-address` default, cosmetic), but the Service actually
backing it is a `LoadBalancer` stuck at `<pending>` — no real external IP.
Meanwhile the pre-existing Envoy `Gateway` already has one:

```
$ kubectl get gateway -A
NAMESPACE              NAME   CLASS           ADDRESS         PROGRAMMED   AGE
envoy-gateway-system   main   envoy-gateway   169.58.222.25   True         10d
```

## 3. Apply the TransferGW

```yaml
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: sample-app-migration
  namespace: transfergw
spec:
  selector:
    namespaces: [demo]
    ingressSelector:
      matchLabels:
        migrate: "true"
  conversion:
    gatewayClass: envoy-gateway
    generateGateway: true
    targetNamespace: transfergw
  rollout:
    mode: gradual
    requireApproval: true
    approvedPercentage: 0
```

Chose `mode: gradual` + `requireApproval: true` deliberately: this is the
feature that got shipped this session (README 3.6,
[docs/DEMO.md](docs/DEMO.md)) and had only ever run against
`sigs.k8s.io/controller-runtime`'s fake client in unit tests — the whole
point of this report is to check whether it holds up against a real API
server, not to re-confirm the parts already proven (immediate rollout,
basic conversion) in prior sessions.

```
$ kubectl apply -f transfergw-cr.yaml
transfergw.transfergw.t-1.dev/sample-app-migration created

$ kubectl get transfergw -n transfergw sample-app-migration -o yaml
status:
  conditions:
  - message: Gateway is receiving 0% of traffic
    reason: RolloutInProgress
    status: "False"
    type: Ready
  pendingPlan:
    nextPercentage: 25
    routeChanges:
    - action: modify
      route: demo/sample-app
  phase: Canary
  processed:
    converted: 1
    total: 1
  resources:
    gateways: 1
    httpRoutes: 1
  trafficRouting:
    ingress: 100
```

Correct on the first read: one Ingress converted, `completionPercentage`
absent (0, held), `pendingPlan.nextPercentage: 25` — gradual mode's default
initial canary step — computed and published without being applied, exactly
as designed.

One thing was wrong here, though: `routeChanges` says `action: modify` for
a route that was just created for the first time. That should have said
`add`. Investigated next rather than waved off.

## 4. A real bug, found live, fixed live

Forced a second reconcile (`kubectl annotate ... force-reconcile=$(date +%s)`)
to see if `modify` was a one-off artifact of a duplicate reconcile firing
right after creation:

```
$ kubectl get transfergw -n transfergw sample-app-migration -o jsonpath='{.status.pendingPlan}'
{"nextPercentage": 25, "routeChanges": [{"action": "modify", "route": "demo/sample-app"}]}
```

Still `modify`, unchanged, on a reconcile where nothing about the Ingress
or the TransferGW spec had changed. That ruled out "duplicate event on
creation" and pointed at a permanent, repeating false diff.

**Root cause**: `kubectl get httproute -n demo sample-app -o yaml` showed
the API server had filled in `group: ""`, `kind: Service`, and `weight: 1`
on the `backendRef`, and `group`/`kind` on the `parentRef` — the Gateway
API CRD's own defaulting for fields this project's conversion code
(`internal/conversion/route.go`, `internal/conversion/engine.go`,
`internal/conversion/grpc.go`) was leaving `nil`. `applyRoute` builds a
fresh in-memory object every reconcile and does `route.Spec = desired.Spec`
inside `controllerutil.CreateOrUpdate`'s mutate callback; `CreateOrUpdate`
compares the object *before* and *after* that mutate call, entirely
client-side, before any round-trip to the server. Since the freshly-fetched
object carried the server's defaults and the freshly-converted `desired.Spec`
didn't, the two were never equal — every single reconcile, forever — so
`CreateOrUpdate` reported `Updated` every time even though the route's
actual meaning never changed.

This was invisible to the existing unit test suite
(`internal/controller/transfergw_controller_test.go`, which does catch
exactly this class of thing via `TestReconcileSecondPassReportsNoRouteChanges`)
because `sigs.k8s.io/controller-runtime/pkg/client/fake` does not run CRD
defaulting the way a real API server does. That gap between the fake-client
suite and reality is precisely why this project also carries a real
`test/e2e` suite against a live cluster (`db0dc35`, an earlier commit) — and
precisely why this manual run mattered.

**Fix** (`internal/conversion/route.go`, `engine.go`, `grpc.go`): set
`Group`, `Kind`, and `Weight` explicitly to the same values the Gateway API
CRD schema itself defaults them to (`""`/`Service`/`1` on a `BackendRef`;
`gateway.networking.k8s.io`/`Gateway` on a `ParentReference`), so the
in-memory object already matches what the server would produce — no more
false diff to detect. Committed as `34f7ffa`.

```
$ git commit -m "fix: set Gateway API's own defaults explicitly to stop perpetual route updates"
$ git push origin main
$ gh run watch <build run>
✓ main Build and Push Image
```

New image `thev1ndu/transfergw:sha-34f7ffa` built and pushed by the same CI
workflow. Upgraded the live release:

```
$ helm upgrade transfergw ./chart/transfergw -n transfergw \
    --set image.tag=sha-34f7ffa --set enableWebhooks=false --wait
Release "transfergw" has been upgraded.

$ kubectl annotate transfergw -n transfergw sample-app-migration force-reconcile=1 --overwrite
$ kubectl get transfergw -n transfergw sample-app-migration -o jsonpath='{.status.pendingPlan}'
{"nextPercentage": 25}

$ kubectl annotate transfergw -n transfergw sample-app-migration force-reconcile=2 --overwrite
$ kubectl get transfergw -n transfergw sample-app-migration -o jsonpath='{.status.pendingPlan}'
{"nextPercentage": 25}
```

`routeChanges` is gone from the output on both reconciles after the fix
(the field is `omitempty` and empty once nothing actually changed) —
confirmed fixed, live, on the same cluster and the same `TransferGW` object
the bug was found on. Unit tests re-run afterward (`go test ./...`) still
pass, including the pre-existing `TestReconcileSecondPassReportsNoRouteChanges`,
which never caught this and still won't catch a regression of this
specific kind without a real API server — a known, stated limitation of
that test, not a false confidence signal.

## 5. A genuine limitation: the new Gateway has no public IP either

```
$ kubectl get gateway -n transfergw sample-app-migration-gateway
NAME                           CLASS           ADDRESS   PROGRAMMED   AGE
sample-app-migration-gateway   envoy-gateway             False        6m38s
```

```
$ kubectl get gateway -n transfergw sample-app-migration-gateway -o yaml
status:
  conditions:
  - type: Programmed
    status: "False"
    reason: AddressNotAssigned
    message: No addresses have been assigned to the Gateway
```

```
$ kubectl get ciliumloadbalancerippool
NAME          DISABLED   CONFLICTING   IPS AVAILABLE   AGE
node-public   false      False         0               10d

$ kubectl get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=sample-app-migration-gateway -o yaml
status:
  conditions:
  - type: cilium.io/IPAMRequestSatisfied
    status: "False"
    reason: out_of_ips
    message: All enabled CiliumLoadBalancerIPPools that match this service ran out of allocatable IPs
```

Not a TransferGW bug: this cluster's single Cilium LB IP pool has exactly
one address, already consumed by the pre-existing `main` Gateway. A newly
generated Gateway on this specific cluster cannot get its own separate
public IP — a real capacity constraint many users hit in practice (limited
public IP pools), not something staged for this report. In production,
either the pool has spare capacity or (more realistically, and more in
line with why `spec.conversion.gatewayName`/`generateGateway: false` exist)
a migration attaches to an already-provisioned shared Gateway instead of
requesting a new one per migration.

**Verification workaround used**: the generated Gateway's own Service still
got a real `NodePort` (`32351`) even without a `LoadBalancer` IP, and this
cluster's node address (`169.58.222.25`, the same address the API server
and `main` Gateway are reachable on) is directly reachable — so the
underlying Envoy proxy, HTTPRoute, and pod were verified for real over
HTTP, not asserted from Kubernetes object state alone. See step 6.

Kept the Gateway inside the `transfergw` namespace, as required by the
two-namespace constraint, rather than attaching to `main` (which lives in
`envoy-gateway-system` and would need a `ReferenceGrant` created there) —
this Gateway address limitation is the direct cost of honoring that
constraint on a single-IP cluster, not a workaround chosen to avoid it.

## 6. Proving real traffic, not just object state

Before approving any rollout percentage:

```
$ curl -sI -H "Host: sample-app.cncf-demo.local" http://169.58.222.25:32351/
HTTP/1.1 200 OK
server: nginx/1.27.5
```

The `HTTPRoute` and generated `Gateway` were already serving real traffic
to the real pod — proof the conversion itself is correct — independent of
`completionPercentage`, which is exactly this project's documented design:
routes are always kept in sync regardless of the rollout gate; only the
percentage a DNS/LB layer would read is held back (see the
`rolloutPercentage` comment in `internal/controller/transfergw_controller.go`,
and [docs/DEMO.md](docs/DEMO.md)).

## 7. Approving the rollout, declaratively

```
$ kubectl patch transfergw -n transfergw sample-app-migration --type=merge \
    -p '{"spec":{"rollout":{"approvedPercentage":25}}}'

$ kubectl get transfergw -n transfergw sample-app-migration \
    -o jsonpath='{"phase: "}{.status.phase}{"\ncompletionPercentage: "}{.status.completionPercentage}{"\npendingPlan: "}{.status.pendingPlan}'
phase: Canary
completionPercentage: 25
pendingPlan: {"currentPercentage":25,"nextPercentage":25}
```

`completionPercentage` moved from held-at-0 to 25 the moment
`approvedPercentage` matched the previously-published `nextPercentage` —
no CLI, no separate apply step, just a field patch on the CR, exactly the
"plan → apply split entirely through the CRD" this feature was built to
provide. `pendingPlan.nextPercentage` staying at 25 afterward is correct,
not stale: gradual mode's next step needs real elapsed time
(`canaryPercentage`'s step duration, default 1h) — approval can hold the
rollout back below the schedule, but never push it ahead of what the
schedule has actually reached.

To demonstrate reaching full completion without an hour's wait, promoted
the same migration to `immediate` and approved 100%, the realistic
"operator has reviewed the canary and decided to finish the rollout" case:

```
$ kubectl patch transfergw -n transfergw sample-app-migration --type=merge \
    -p '{"spec":{"rollout":{"mode":"immediate","approvedPercentage":100}}}'

$ kubectl get transfergw -n transfergw sample-app-migration \
    -o jsonpath='{"phase: "}{.status.phase}{"\ncompletionPercentage: "}{.status.completionPercentage}'
phase: Complete
completionPercentage: 100
```

```
$ curl -sI -H "Host: sample-app.cncf-demo.local" http://169.58.222.25:32351/
HTTP/1.1 200 OK
```

Real traffic still flowing, correctly, at the end of the rollout.

## 8. Final state

```
$ kubectl get ingress,deploy,svc -n demo
NAME                                   CLASS   HOSTS                        ADDRESS     PORTS   AGE
ingress.networking.k8s.io/sample-app   nginx   sample-app.cncf-demo.local   localhost   80      9m9s
NAME                         READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/sample-app   1/1     1            1           9m10s
NAME                 TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)   AGE
service/sample-app   ClusterIP   10.107.132.113   <none>        80/TCP    9m10s

$ kubectl get transfergw,gateway,httproute -n transfergw
NAME                                                 PHASE      PROGRESS   INGRESS   GATEWAY   AGE
transfergw.transfergw.t-1.dev/sample-app-migration   Complete   100                  100       8m31s
NAME                                                             CLASS           ADDRESS   PROGRAMMED   AGE
gateway.gateway.networking.k8s.io/sample-app-migration-gateway   envoy-gateway             False        8m32s
```

The original `Ingress` was never deleted or edited by TransferGW (it never
touches the source object) — the generated `HTTPRoute`/`Gateway` exist
alongside it, and `status.rollbackReady: true` on the `TransferGW` records
that reverting is still an option.

## Summary

| Check | Result |
|---|---|
| Legacy path (nginx) has no public IP | Confirmed real, not staged (`<pending>` LoadBalancer) |
| Ingress selected by label | Yes — 1/1 converted |
| `Gateway`/`HTTPRoute` generated | Yes, correct `parentRef`/`backendRef` |
| `requireApproval` holds traffic percentage | Yes — held at 0 until approved |
| `status.pendingPlan` previews the next step | Yes — `nextPercentage: 25` before approval |
| Approving via `kubectl patch` advances the rollout | Yes — 0 → 25 → 100, no CLI needed |
| Real HTTP traffic through the generated Gateway | Yes — `HTTP 200` from the real pod, before and after rollout |
| Route-change tracking (`routeChanges`) | **Bug found and fixed live**: false permanent `modify` due to missing Gateway API defaults; root-caused, fixed, re-verified on the same cluster |
| Two-namespace constraint (`demo`, `transfergw`) | Honored — no objects created in any other namespace |
| Validating webhook | Not exercised live this run (needs a cert not provisioned for this test); covered separately by `internal/controller/webhook`'s unit suite |
| New Gateway's own public IP | Not available on this cluster (single-IP LB pool, already consumed) — a real environment limit, verified instead via NodePort + real HTTP response |

This run found a real, previously-invisible bug (perpetual false route
diffs against a real API server) that the project's own fake-client unit
suite could not have caught, fixed it, and re-verified the fix on the same
live cluster and the same migration object — which is the actual value a
live end-to-end run is supposed to provide, beyond what a green CI badge
already claims.

# DEMO: Declarative plan/diff

This walks through **spec.rollout.requireApproval** (README 3.6): a
`terraform plan` / `terraform apply` split done entirely through the
`TransferGW` CR, no separate CLI. The mechanics below are exactly what
`internal/controller/transfergw_controller_test.go` exercises
(`TestReconcileRequireApprovalHoldsAtApprovedPercentage`,
`TestReconcileApprovingPercentageAdvancesRollout`,
`TestReconcileSecondPassReportsNoRouteChanges`) — this file just walks the
same behavior on a live cluster.

## The idea

Every reconcile, the controller still computes what the rollout schedule
(`immediate`/`gradual`/`canary`) *wants* the Gateway's traffic share to be.
Normally that value is applied immediately. With `requireApproval: true`,
it's held at `approvedPercentage` instead, and the schedule's actual answer
is published on `status.pendingPlan.nextPercentage` so you can see it before
it takes effect.

The generated `Gateway`/`HTTPRoute` objects are **not** gated — they're
always kept in sync with the selected Ingresses regardless of approval,
because a route object existing doesn't by itself move client traffic (see
the `rolloutPercentage` comment in the controller: that's a DNS/load-balancer
concern outside this controller). What *is* gated is the one number an
external system would read to decide how much traffic to send — the same
thing `status.completionPercentage` has always reported, now with a
review step in front of it. `status.pendingPlan.routeChanges` records what
route adds/updates/removals happened on the reconcile that produced it —
informational, not something you approve separately.

## 1. Apply a gated migration

Uses the same demo Ingress as [docs/TESTING.md](docs/TESTING.md) — see there
if you need it stood up first.

```bash
kubectl apply -f - <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: demo-migration
  namespace: demo
spec:
  selector:
    namespaces: [demo]
    ingressSelector: {matchLabels: {migrate: "true"}}
  conversion:
    gatewayClass: eg
    generateGateway: true
  rollout:
    mode: immediate
    requireApproval: true
    approvedPercentage: 0
EOF
```

## 2. See the plan without anything moving

```bash
kubectl get transfergw demo-migration -n demo -o yaml
```

```yaml
status:
  phase: Canary
  completionPercentage: 0        # held — immediate mode alone would set this to 100
  pendingPlan:
    nextPercentage: 100          # what immediate mode actually computed
    currentPercentage: 0
    routeChanges:
      - action: add
        route: demo/sample-nginx
```

Traffic split hasn't moved (`completionPercentage: 0`); the `HTTPRoute`
itself already exists (`kubectl get httproute -n demo` will show it) — only
the percentage is held.

## 3. Review, then approve declaratively

No CLI, just a field bump:

```bash
kubectl patch transfergw demo-migration -n demo --type=merge \
  -p '{"spec":{"rollout":{"approvedPercentage":100}}}'
```

## 4. Confirm it advanced

```bash
kubectl get transfergw demo-migration -n demo -o yaml
```

```yaml
status:
  phase: Complete
  completionPercentage: 100
  pendingPlan:
    nextPercentage: 100
    currentPercentage: 100
    routeChanges: []            # nothing changed this reconcile - already in sync
```

`routeChanges` is empty here: the route was created back in step 1 and
hasn't needed an update since, so this reconcile has nothing new to report
(confirmed by `TestReconcileSecondPassReportsNoRouteChanges`).

## Gradual/canary rollouts

The same fields work with `mode: gradual` or `mode: canary` — each
reconcile, `pendingPlan.nextPercentage` shows the next scheduled step, and
`approvedPercentage` has to be bumped to at least that value to let it
through. Leaving `approvedPercentage` behind holds the rollout at its last
approved step indefinitely, same as a stale `terraform plan` never getting
applied.

## Caveat: health-based rollback still overrides approval

A migration with `spec.monitoring` thresholds configured (see
[docs/TESTING.md](docs/TESTING.md)) can still roll back to 0% on a breach
even while `requireApproval` is set — safety isn't something you can approve
your way past. `pendingPlan.nextPercentage` reflects the schedule's
unclamped answer at the moment of that reconcile; if `evaluateHealth` moves
between reconciles, the next plan can differ even with no spec change, the
same way a live `terraform plan` can go stale if the underlying
infrastructure drifts before you `apply`.

## Where this lives in code

- `spec.rollout.requireApproval` / `approvedPercentage` —
  `api/v1beta1/transfergw_types.go`, `RolloutSpec`.
- `status.pendingPlan` — same file, `PendingPlanStatus`/`RouteChange`.
- Gating logic — `internal/controller/transfergw_controller.go`, the
  `scheduledPercentage`/`RequireApproval` block right after the PreRollout
  hook check.
- Route-change tracking — `routeChangeFor` (reads the
  `controllerutil.OperationResult` that `applyRoute`/`applyGRPCRoute` already
  got back from `CreateOrUpdate`) and `pruneOrphanedRoutes`' removal entries.

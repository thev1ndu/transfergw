# Testing TransferGW on a Cluster: nginx Ingress → Envoy Gateway

A ~15 minute walkthrough that installs TransferGW from its published GitHub package,
stands up a sample nginx behind an ingress-nginx Ingress, and migrates it to Envoy
Gateway.

Everything here uses the published artifacts — no repository clone, no local build — and
runs against **an existing cluster**.

| Artifact | Location |
|---|---|
| Controller image | `thev1ndu/transfergw` on Docker Hub |
| Helm chart | `oci://ghcr.io/thev1ndu/helm-charts/transfergw` version `1.0.0` |

Both are public; no registry login is needed.

> **What "migrate" means here.** TransferGW reads your Ingresses and generates the
> equivalent Gateway API resources — a `Gateway` plus one `HTTPRoute` per Ingress. Both
> paths then serve the same workload side by side, and you verify the Gateway path before
> cutting over. TransferGW does **not** move live client traffic between them; that stays
> a DNS or load-balancer change you make when you're satisfied. The original Ingress is
> left untouched throughout.

---

## Prerequisites

- An existing Kubernetes cluster, 1.26 or newer
- `kubectl` pointed at it, with **cluster-admin** — the install creates CRDs, a
  ClusterRole and a ClusterRoleBinding
- `helm` 3.8 or newer, for OCI registry support

Confirm your context before you start, so nothing lands in the wrong cluster:

```bash
kubectl config current-context
kubectl version -o json | grep -m1 gitVersion
```

Everything is created in three namespaces — `demo`, `transfergw`, and
`envoy-gateway-system` — plus `ingress-nginx` if you don't already have it. Nothing
outside those is touched.

> **All HTTP checks in this guide run from inside the cluster** with an explicit `Host:`
> header, against Service DNS. That means they work identically on EKS, GKE, AKS, on-prem
> and local clusters, and need no LoadBalancer, no Ingress IP, and no DNS record. The
> hostname `demo.test` is never resolved — `.test` is reserved by IANA precisely for this.

---

## 1. Deploy a sample nginx

```bash
kubectl create namespace demo

cat <<'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-nginx
  namespace: demo
spec:
  replicas: 2
  selector:
    matchLabels:
      app: sample-nginx
  template:
    metadata:
      labels:
        app: sample-nginx
    spec:
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          ports:
            - containerPort: 80
              name: http
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 128Mi
---
apiVersion: v1
kind: Service
metadata:
  name: sample-nginx
  namespace: demo
spec:
  selector:
    app: sample-nginx
  ports:
    - name: http
      port: 80
      targetPort: http
EOF

kubectl rollout status deployment/sample-nginx -n demo
```

---

## 2. Ensure ingress-nginx is present

Your cluster may already run it. Check before installing anything:

```bash
kubectl get ingressclass
```

If an `nginx` class is listed, **skip the install** and go straight to creating the
Ingress below. Otherwise:

```bash
helm upgrade --install ingress-nginx ingress-nginx \
  --repo https://kubernetes.github.io/ingress-nginx \
  --namespace ingress-nginx --create-namespace

kubectl wait --namespace ingress-nginx \
  --for=condition=Ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=300s
```

> On a cluster with no LoadBalancer provider the controller Service stays `<pending>` for
> an external IP. That is fine — every check in this guide goes through the Service's
> cluster IP, not an external one.

### Create the Ingress

Two details matter to TransferGW: the **`migrate: "true"` label**, which the migration
selects on, and the **rewrite-target annotation**, which exercises annotation translation.

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: sample-nginx
  namespace: demo
  labels:
    migrate: "true"
  annotations:
    nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  ingressClassName: nginx
  rules:
    - host: demo.test
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: sample-nginx
                port:
                  number: 80
EOF

kubectl get ingress -n demo
```

### Establish the baseline

Resolve the ingress controller Service, then send a request through it from inside the
cluster:

```bash
ING_NS=ingress-nginx
ING_SVC=$(kubectl get svc -n $ING_NS \
  -l app.kubernetes.io/component=controller \
  -o jsonpath='{.items[0].metadata.name}')

kubectl run curl-ingress --rm -i --restart=Never -n demo \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -o /dev/null -w 'ingress: %{http_code}\n' \
  -H 'Host: demo.test' "http://${ING_SVC}.${ING_NS}.svc.cluster.local"
```

> If your cluster already had ingress-nginx in a different namespace, set `ING_NS`
> accordingly — `kubectl get pods -A | grep ingress-nginx` will find it.

Expect `ingress: 200`. **This is the "before" state.** It must keep returning 200 for the
rest of the walkthrough — if it ever stops, the migration has disturbed something it
shouldn't have.

---

## 3. Install Envoy Gateway

```bash
helm install eg oci://docker.io/envoyproxy/gateway-helm \
  --namespace envoy-gateway-system --create-namespace

kubectl wait --namespace envoy-gateway-system \
  --for=condition=Available deployment/envoy-gateway --timeout=300s
```

The chart installs the Gateway API CRDs for you. Verify:

```bash
kubectl get crd | grep gateway.networking.k8s.io
```

You should see `gateways`, `httproutes`, `gatewayclasses` and friends. If they're absent,
install them explicitly and re-run the wait:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.2/standard-install.yaml
```

> **If the cluster already has a Gateway API implementation** (Istio, Contour, NGINX
> Gateway Fabric), you can skip Envoy Gateway entirely and use that controller's
> GatewayClass instead. Just substitute its name in step 5.

Now make sure a GatewayClass exists:

```bash
kubectl get gatewayclass
```

If the list is empty, create one:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: eg
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
EOF
```

Note whichever name you end up with — it goes into `conversion.gatewayClass` in step 5.
The rest of this guide assumes `eg`.

---

## 4. Install TransferGW from the published chart

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --version 1.0.0 \
  --namespace transfergw --create-namespace
```

The chart creates the `transfergw` namespace, the CRD, RBAC, and a 2-replica controller
Deployment, pulling `thev1ndu/transfergw:latest` from Docker Hub by default with
`imagePullPolicy: IfNotPresent`.

> **Want a reproducible, pinned image instead of `latest`?** Override the tag:
>
> ```bash
> --set image.tag=sha-a8fe786
> ```
>
> `sha-<commit>` tags are immutable — `latest` moves with every push to `main`, so a
> node that already cached an older `latest` layer can keep running stale code under
> `IfNotPresent` until it's forced to re-pull. Check
> [Tags](https://hub.docker.com/r/thev1ndu/transfergw/tags) for available `sha-*` tags,
> or add `--set image.pullPolicy=Always` if you'd rather always track `latest` fresh.

On a small or single-node cluster you may prefer one replica:

```bash
helm install transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --version 1.0.0 \
  --namespace transfergw --create-namespace \
  --set replicaCount=1
```

Verify:

```bash
kubectl get crd transfergws.transfergw.t-1.dev
kubectl rollout status deployment/transfergw-controller -n transfergw
kubectl logs -n transfergw deployment/transfergw-controller --tail=20
```

`rollout status` must report both replicas available, and the log should end with
`starting manager`. If the pods restart in a loop instead, see
[controller crash-loops](#controller-crash-loops-on-failed-liveness-probe) below.

> **Chart versioning caveat.** The chart version is not bumped per commit — `1.0.0` is
> republished on every push to `main`. Two pushes can therefore ship different content
> under the same version. Pin the image tag as above for anything reproducible.

---

## Preview a migration before applying it

`kubectl apply --dry-run` on a `TransferGW` cannot show you the `Gateway`/`HTTPRoute` it
would generate. Client-side dry-run never reaches the API server, and server-side
dry-run validates but never persists the object — with nothing persisted there's no
watch event, and the controller only reconciles on watch events. No dry-run of the
`TransferGW` itself can trigger the logic that produces the generated resources.

`cmd/preview` runs that exact selection-and-conversion logic locally, against your
current kubeconfig context, and prints the resulting manifests to stdout — nothing is
created, not even the `TransferGW`.

Set up an Ingress to preview against, in its own namespace so it doesn't collide with
the rest of this walkthrough:

```bash
kubectl create namespace demo-test

cat <<'EOF' | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: sample-nginx
  namespace: demo-test
  labels:
    migrate: "true"
  annotations:
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  ingressClassName: nginx
  rules:
    - host: test.2take1.nl
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: sample-nginx
                port:
                  number: 80
EOF
```

Write the `TransferGW` you're considering, but don't apply it:

```bash
cat > /tmp/preview-migration.yaml <<'EOF'
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: preview-migration
  namespace: demo-test
spec:
  selector:
    namespaces:
      - demo-test
    ingressSelector:
      matchLabels:
        migrate: "true"
  conversion:
    gatewayClass: eg
    generateGateway: true
  rollout:
    mode: canary
    strategy: percentage
EOF
```

Preview it:

```bash
make preview FILE=/tmp/preview-migration.yaml
```

or without the Makefile:

```bash
go run ./cmd/preview -f /tmp/preview-migration.yaml
```

This prints the `Gateway` (with a listener derived from `sample-nginx`) and the
`HTTPRoute` `cmd/preview` would create — including the `RequestRedirect` filter
`ssl-redirect: "true"` translates to — plus any conversion issues on stderr. Nothing
is written to the cluster; delete the namespace when done:

```bash
kubectl delete namespace demo-test
```

---

## 5. Run the migration

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: demo-migration
  namespace: transfergw
spec:
  selector:
    namespaces:
      - demo
    ingressSelector:
      matchLabels:
        migrate: "true"
    ingressClasses:
      - nginx
  conversion:
    gatewayClass: eg
    targetNamespace: transfergw
    generateGateway: true
    validationMode: strict
  rollout:
    mode: immediate
EOF
```

`mode: immediate` completes in one pass, which is what you want for a test. Use
`mode: canary` to watch the percentage climb over time instead — see
[Canary mode](#canary-mode).

> **Scoping note.** `selector.namespaces` is restricted to `demo` here on purpose. Leaving
> it empty selects **every namespace in the cluster**, which on a shared cluster would
> generate routes for Ingresses you did not intend to touch. Keep it explicit.

Check the result:

```bash
kubectl get tgw -n transfergw
```

```
NAME             PHASE      PROGRESS   INGRESS   GATEWAY   AGE
demo-migration   Complete   100        0         100       8s
```

`Phase: Complete` and `PROGRESS: 100` mean every selected Ingress converted.

---

## 6. Inspect what was generated

**The Gateway**, in `conversion.targetNamespace`, named `<migration-name>-gateway`:

```bash
kubectl get gateway -n transfergw
kubectl get gateway demo-migration-gateway -n transfergw -o yaml
```

**The HTTPRoute**, created *beside its source Ingress* in `demo` — not in the target
namespace. That keeps the backend Service reference in-namespace, so no `ReferenceGrant`
is needed:

```bash
kubectl get httproute -n demo
kubectl get httproute sample-nginx -n demo -o yaml
```

You should see the Ingress translated field for field:

```yaml
spec:
  parentRefs:
    - name: demo-migration-gateway
      namespace: transfergw        # qualified, since the Gateway is elsewhere
  hostnames:
    - demo.test                    # from spec.rules[].host
  rules:
    - matches:
        - path:
            type: PathPrefix       # from pathType: Prefix
            value: /
      filters:
        - type: URLRewrite         # translated from the rewrite-target annotation
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: sample-nginx
          port: 80
```

Generated resources are labelled, which is how the operator tracks them:

```bash
kubectl get httproute,gateway -A -l transfergw.t-1.dev/managed-by=demo-migration
```

**Conversion report** — anything that couldn't be translated cleanly lands on status
rather than only in the log:

```bash
kubectl describe tgw demo-migration -n transfergw
```

```
Status:
  Phase:                  Complete
  Completion Percentage:  100
  Processed:
    Total:      1
    Converted:  1
    Failed:     0
  Resources:
    Gateways:     1
    Http Routes:  1
```

---

## 7. Verify the Gateway actually serves the app

First, check that Envoy accepted it and pushed config to the data plane. Look at the
**listener** conditions, not just the top-level ones:

```bash
kubectl get gateway demo-migration-gateway -n transfergw \
  -o jsonpath='{range .status.listeners[*]}{.name}:{"\n"}{range .conditions[*]}  {.type}={.status} ({.reason}){"\n"}{end}{end}'
```

Expect `Accepted=True`, `Programmed=True` and `ResolvedRefs=True` on the `http` listener.
`ResolvedRefs=True` is the one that confirms your generated HTTPRoute attached.

> **Top-level `Programmed: False` with `AddressNotAssigned` is expected on many clusters
> — it does not mean the migration failed.** Envoy Gateway derives that condition from the
> proxy Service's *external* IP. On a cluster with no LoadBalancer provider (kind without
> MetalLB, bare metal, most local setups) the Service stays `<pending>` forever, so no
> address is ever assigned.
>
> The proxy still runs and still routes on its cluster IP, which is what the check below
> uses. Confirm with `kubectl get svc -n envoy-gateway-system -l
> gateway.envoyproxy.io/owning-gateway-name=demo-migration-gateway` — a `<pending>`
> `EXTERNAL-IP` alongside a `2/2 Running` proxy pod is the signature. Treat the `200`
> below as the real acceptance signal, not this condition.

Envoy Gateway creates a Service for each Gateway; find it and send a request through it:

```bash
GW_SVC=$(kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=demo-migration-gateway \
  -o jsonpath='{.items[0].metadata.name}')

kubectl run curl-gateway --rm -i --restart=Never -n demo \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -o /dev/null -w 'gateway: %{http_code}\n' \
  -H 'Host: demo.test' "http://${GW_SVC}.envoy-gateway-system.svc.cluster.local"
```

Expect `gateway: 200`.

**The acceptance check** — re-run the baseline from step 2 and confirm both paths serve
at once:

```bash
kubectl run curl-ingress --rm -i --restart=Never -n demo \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -o /dev/null -w 'ingress: %{http_code}\n' \
  -H 'Host: demo.test' "http://${ING_SVC}.${ING_NS}.svc.cluster.local"
```

Both `200` means the migration succeeded: the Gateway API path works and the Ingress was
never disturbed. Cutting traffic over is now a DNS or load-balancer change on your side.

---

## 8. Confirm it reconciles continuously

The controller watches Ingresses, so edits propagate without touching the TransferGW.

Add a path to the Ingress:

```bash
kubectl patch ingress sample-nginx -n demo --type=json \
  -p='[{"op":"add","path":"/spec/rules/0/http/paths/-","value":{
        "path":"/api","pathType":"Prefix",
        "backend":{"service":{"name":"sample-nginx","port":{"number":80}}}}}]'

sleep 3
kubectl get httproute sample-nginx -n demo \
  -o jsonpath='{.spec.rules[*].matches[*].path.value}{"\n"}'
```

Expect `/ /api`.

Now deselect the Ingress and watch the route get pruned:

```bash
kubectl label ingress sample-nginx -n demo migrate-
sleep 3
kubectl get httproute -n demo
```

Expect `No resources found`. Re-label to bring it back:

```bash
kubectl label ingress sample-nginx -n demo migrate=true
```

---

## Canary mode

With `mode: canary`, `completionPercentage` starts at `canary.initialPercentage` and
advances by `canary.increment` every `canary.stepDuration`, measured from the resource's
creation timestamp:

```yaml
spec:
  rollout:
    mode: canary
    canary:
      initialPercentage: 25
      increment: 25
      stepDuration: 1h
```

The Gateway and HTTPRoutes are created immediately and in full — the percentage describes
the *intended* traffic split on `status.trafficRouting`, which is the signal for your
DNS/LB cutover. Nothing in the cluster enforces it.

Pause at any time:

```bash
kubectl patch tgw demo-migration -n transfergw --type=merge \
  -p '{"spec":{"rollout":{"paused":true}}}'
```

---

## Things worth testing deliberately

Each of these should produce a clear entry under `status.issues` rather than a silent
failure. They're the cases most likely to bite on a real Ingress.

| Change to the Ingress | Expected behaviour |
|---|---|
| Backend referenced by port **name** instead of number | Conversion fails for that Ingress; `Phase: Failed`, error on status. HTTPRoute `backendRefs` require a numeric port. |
| `pathType: ImplementationSpecific` with a regex like `/api/v1/.*` | Converted to a prefix match on `/api/v1` with a warning about the approximation. |
| `rewrite-target: /$1` (capture group) | Warning; no `URLRewrite` filter emitted, since Gateway API can't express it. |
| `nginx.ingress.kubernetes.io/limit-rps` | Warning — no core Gateway API equivalent. Needs an Envoy Gateway policy CRD. |
| `nginx.ingress.kubernetes.io/auth-url` | Warning, same reason. |
| A TLS block | Info note; the Gateway gets an HTTPS listener per distinct secret. |

Once the sample passes, the more useful test is pointing a migration at one of your own
namespaces — with `rollout.paused: true` first if you want to inspect before anything is
created.

---

## Known limitations

Be aware of these before judging results:

- **No traffic steering.** `status.trafficRouting` is advisory. Both paths serve fully.
- **No health monitoring or auto-rollback.** `spec.monitoring` validates and persists, but
  nothing reads it.
- **No lifecycle actions.** `pauseOriginalIngress`, `backupOriginal` and `cleanupOnSuccess`
  under `spec.lifecycle` are accepted and ignored.
- **No admission webhooks.** Invalid specs are caught by CRD schema validation only.
- **Rate-limit, auth and cert-manager annotations are reported, not translated** — by
  design, since core Gateway API has no equivalent.

---

## Troubleshooting

**No HTTPRoute appears**

```bash
kubectl describe tgw demo-migration -n transfergw
kubectl logs -n transfergw deployment/transfergw-controller --tail=100
```

Usual causes: the Ingress is missing `migrate=true`; its namespace isn't in
`selector.namespaces`; its class isn't in `selector.ingressClasses`; or the backend uses a
named Service port.

**`Phase: Failed`**

At least one selected Ingress could not be converted. `status.issues` names the Ingress
and the reason. The percentage is deliberately held at 0 rather than advancing over a
partial migration.

**`Programmed: False` with `AddressNotAssigned`**

Not a failure. Your cluster has no LoadBalancer provider, so the proxy Service never gets
an external IP and Envoy Gateway cannot assign the Gateway an address. Routing still
works on the cluster IP.

```bash
kubectl get gateway demo-migration-gateway -n transfergw \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} ({.reason}){"\n"}{end}'

kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=demo-migration-gateway
```

`Accepted=True` plus a `<pending>` `EXTERNAL-IP` confirms it. Use the in-cluster `curl`
from step 7 as your acceptance check. To get a real address, install MetalLB, or set the
proxy Service to `NodePort` via an Envoy Gateway `EnvoyProxy` resource.

**`Programmed: False` for any other reason**

```bash
kubectl get gateway demo-migration-gateway -n transfergw -o yaml | grep -A15 conditions
kubectl logs -n envoy-gateway-system deployment/envoy-gateway --tail=50
```

If `Accepted` is also `False`, it is usually a missing or mismatched GatewayClass —
confirm `conversion.gatewayClass` matches `kubectl get gatewayclass`.

**Controller pod `ImagePullBackOff`**

The image is public, so this normally means egress to Docker Hub (`registry-1.docker.io`)
is blocked. The Helm chart itself still pulls from `ghcr.io` (OCI registry), so check
both if egress is filtered. Mirror the image:

```bash
docker pull thev1ndu/transfergw:latest
docker tag thev1ndu/transfergw:latest <your-registry>/transfergw:latest
docker push <your-registry>/transfergw:latest

helm upgrade transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --version 1.0.0 -n transfergw \
  --set image.repository=<your-registry>/transfergw \
  --set image.tag=latest
```

**Controller crash-loops on failed liveness probe**

Symptom — the pods restart every ~30s, and events show:

```
Liveness probe failed: Get "http://10.x.x.x:8081/healthz": connect: connection refused
```

Check the logs. If the manager looks healthy (`Successfully acquired lease`,
`Starting workers`, `Serving metrics server`) but there is **no line about the health
probe binding**, you are running a build from before commit `4842aa2` ("bind the health
probe server so the controller stays up"), where the probe listener was never started.
The manager works; nothing answers on `:8081`; the kubelet kills it.

Almost always a stale cached `latest`. Pin to any tag published after that fix, e.g.:

```bash
helm upgrade transfergw oci://ghcr.io/thev1ndu/helm-charts/transfergw \
  --version 1.0.0 -n transfergw \
  --set image.tag=sha-a8fe786

kubectl rollout status deployment/transfergw-controller -n transfergw
```

Confirm which image the pods actually run:

```bash
kubectl get pods -n transfergw -o jsonpath='{.items[*].spec.containers[*].image}{"\n"}'
```

**`helm install` fails with `docker-credential-desktop: executable file not found`**

Helm reads `~/.docker/config.json` for registry credentials. If that file sets
`"credsStore": "desktop"` but Docker Desktop isn't installed or isn't on your `PATH`, the
pull fails before it ever reaches the network — even though these packages are public and
need no authentication. Common on machines that once had Docker Desktop.

Unblock without touching your config:

```bash
export DOCKER_CONFIG=$(mktemp -d) && echo '{"auths":{}}' > $DOCKER_CONFIG/config.json
```

Or fix it permanently by removing the `credsStore` line from `~/.docker/config.json`. If
`auths` is empty, that line is doing nothing but breaking Helm.

**`curl` pod fails with a NetworkPolicy denial**

Clusters with default-deny policies will block the in-cluster checks. Either add a
temporary egress allowance for the `demo` namespace, or run the checks with
`kubectl port-forward` against the two Services instead.

---

## Cleanup

Removes only what this guide created:

```bash
kubectl delete tgw --all -n transfergw
helm uninstall transfergw -n transfergw
helm uninstall eg -n envoy-gateway-system
kubectl delete namespace demo transfergw envoy-gateway-system
kubectl delete crd transfergws.transfergw.t-1.dev
```

Only if **you** installed ingress-nginx in step 2 — leave it alone if it was already
there:

```bash
helm uninstall ingress-nginx -n ingress-nginx
kubectl delete namespace ingress-nginx
```

The Gateway API CRDs are left in place, since other workloads may depend on them.

---

## Feedback

Please report back with: the `kubectl describe tgw` output, the generated HTTPRoute, and
any Ingress in your own services that produced a `status.issues` entry. The annotation
translation surface is the part most likely to need extending.

Repository: https://github.com/thev1ndu/transfergw

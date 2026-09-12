# TransferGW on a Kubernetes Cluster: End-to-End Walkthrough

This guide takes you from an empty machine to a running TransferGW operator watching a
real Ingress. You will:

1. Create a local Kubernetes cluster
2. Deploy a sample nginx workload and expose it with a Service
3. Install an ingress controller and create an Ingress
4. Install the Gateway API CRDs and a Gateway controller
5. Install TransferGW with Helm
6. Create a TransferGW resource to drive the migration

> **Scope note.** Steps 1-6 work end to end: applying a TransferGW makes the operator
> discover the matching Ingresses and generate a Gateway plus one HTTPRoute per Ingress.
> What the operator does *not* do is steer live client traffic between the Ingress and
> the Gateway — that happens at the DNS or load-balancer layer. `status.trafficRouting`
> records the intended split; moving real traffic is your job. See
> [What is and is not implemented](#what-is-and-is-not-implemented).

---

## Prerequisites

| Tool | Purpose | Install |
|---|---|---|
| Docker | Runtime for the kind cluster | https://docs.docker.com/get-docker/ |
| kind | Local Kubernetes cluster | `brew install kind` |
| kubectl | Cluster CLI | `brew install kubectl` |
| helm | Installs the TransferGW chart | `brew install helm` |

Kubernetes 1.26 or later is required. kind's default node image satisfies this.

---

## 1. Create the cluster

The sample Ingress needs to be reachable from your laptop, so create the cluster with a
node that publishes ports 80 and 443.

```bash
cat <<'EOF' > kind-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            node-labels: "ingress-ready=true"
    extraPortMappings:
      - containerPort: 80
        hostPort: 80
        protocol: TCP
      - containerPort: 443
        hostPort: 443
        protocol: TCP
EOF

kind create cluster --name transfergw --config kind-config.yaml
```

Confirm the node is up:

```bash
kubectl cluster-info --context kind-transfergw
kubectl get nodes
```

The `ingress-ready=true` label is what the kind flavour of ingress-nginx schedules
against in step 3. Without it the controller pod stays `Pending`.

---

## 2. Deploy a sample nginx and expose it

Create a namespace for the demo application. The `migrate=true` label on the Ingress in
step 3 is what TransferGW will later select on.

```bash
kubectl create namespace demo
```

Deploy nginx and a Service in front of it:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-nginx
  namespace: demo
  labels:
    app: sample-nginx
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
  type: ClusterIP
  selector:
    app: sample-nginx
  ports:
    - name: http
      port: 80
      targetPort: http
EOF
```

Wait for the pods and verify the Service resolves inside the cluster:

```bash
kubectl rollout status deployment/sample-nginx -n demo

kubectl run curl-test --rm -it --restart=Never -n demo \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -o /dev/null -w '%{http_code}\n' http://sample-nginx.demo.svc.cluster.local
```

A `200` means the Deployment and Service are wired correctly.

---

## 3. Install an ingress controller and create the Ingress

TransferGW migrates *away from* Ingress, so you need a working Ingress first.

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

kubectl wait --namespace ingress-nginx \
  --for=condition=Ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=180s
```

Now create the Ingress. Note the `migrate: "true"` label — this is the hook TransferGW's
selector uses.

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
    - host: demo.localtest.me
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

Verify it serves traffic. `localtest.me` resolves to `127.0.0.1`, so this works without
editing `/etc/hosts`:

```bash
kubectl get ingress -n demo
curl -s -o /dev/null -w '%{http_code}\n' http://demo.localtest.me
```

You should get `200`. This is the "before" state that TransferGW is meant to migrate.

---

## 4. Install Gateway API CRDs and a Gateway controller

The migration target is a Gateway API implementation. Install the CRDs first — version
`v1.6.2` matches the `sigs.k8s.io/gateway-api` dependency pinned in this repository's
`go.mod`.

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.2/standard-install.yaml

kubectl get crd | grep gateway.networking.k8s.io
```

CRDs alone only let you *create* Gateway and HTTPRoute objects; something has to act on
them. Install Envoy Gateway as the implementation:

```bash
helm install eg oci://docker.io/envoyproxy/gateway-helm \
  --namespace envoy-gateway-system --create-namespace

kubectl wait --namespace envoy-gateway-system \
  --for=condition=Available deployment/envoy-gateway \
  --timeout=180s
```

Confirm a GatewayClass exists — this is the name you will reference as
`conversion.gatewayClass`:

```bash
kubectl get gatewayclass
```

---

## 5. Install TransferGW with Helm

### Make the image pullable

The chart defaults to `thev1ndu/transfergw:latest`, built and pushed by the
`Build and Push Image` workflow. **GHCR packages are private by default.** Either make
the package public in the repository's package settings, or create a pull secret:

```bash
kubectl create namespace transfergw

kubectl create secret docker-registry ghcr-creds \
  --namespace transfergw \
  --docker-server=ghcr.io \
  --docker-username=<your-github-username> \
  --docker-password=<a-github-PAT-with-read:packages>
```

### Install the chart

Clone the repository and install from the local path:

```bash
git clone https://github.com/thev1ndu/transfergw.git
cd transfergw

helm install transfergw chart/transfergw \
  --namespace transfergw \
  --create-namespace
```

The chart installs into the `transfergw` namespace by default and creates the namespace
itself, so `--create-namespace` is belt-and-braces.

For a local build instead of GHCR, build the image and load it into kind:

```bash
docker build -f build/Dockerfile -t transfergw:dev .
kind load docker-image transfergw:dev --name transfergw

helm install transfergw chart/transfergw \
  --namespace transfergw --create-namespace \
  --set image.repository=transfergw \
  --set image.tag=dev \
  --set image.pullPolicy=Never
```

### Verify the install

```bash
kubectl get crd transfergws.transfergw.t-1.dev
kubectl get pods -n transfergw
kubectl rollout status deployment/transfergw-controller -n transfergw
kubectl logs -n transfergw deployment/transfergw-controller --tail=50
```

> The chart requests 2 replicas with soft pod anti-affinity. On a single-node kind
> cluster both pods schedule onto the same node, which is fine — the anti-affinity is
> `preferredDuringScheduling`, not `required`. Set `--set replicaCount=1` if you prefer.

Useful overrides:

```bash
helm install transfergw chart/transfergw -n transfergw --create-namespace \
  --set replicaCount=1 \
  --set logLevel=debug \
  --set crd.install=false        # if the CRD is already applied
```

---

## 6. Create the migration resource

Apply a TransferGW that selects the Ingress you labelled in step 3:

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
    mode: canary
    paused: false
EOF
```

Watch it:

```bash
kubectl get transfergw -n transfergw
kubectl get tgw -n transfergw -w          # tgw is the registered short name
kubectl describe transfergw demo-migration -n transfergw
kubectl logs -n transfergw deployment/transfergw-controller -f
```

Within a few seconds the operator reports what it did:

```
NAME             PHASE      PROGRESS   INGRESS   GATEWAY   AGE
demo-migration   Canary     25         75        25        12s
```

`kubectl describe` shows the conversion statistics and any issues raised while
translating annotations:

```bash
kubectl describe transfergw demo-migration -n transfergw
```

```
Status:
  Phase:                  Canary
  Completion Percentage:  25
  Processed:
    Total:      1
    Converted:  1
    Failed:     0
  Resources:
    Gateways:     1
    Http Routes:  1
  Issues:
    Ingress:         demo/sample-nginx
    Severity:        info
    Issue:           annotation "nginx.ingress.kubernetes.io/rewrite-target" ...
```

---

## 7. Inspect what the operator generated

The Gateway is created in `conversion.targetNamespace`, named `<migration>-gateway`:

```bash
kubectl get gateway -n transfergw
kubectl get gateway demo-migration-gateway -n transfergw -o yaml
```

Each HTTPRoute is created **beside its source Ingress**, not in the target namespace, so
the route can reference the backend Service without a cross-namespace ReferenceGrant:

```bash
kubectl get httproute -A
kubectl get httproute sample-nginx -n demo -o yaml
```

You should see the Ingress host carried over as `spec.hostnames`, the path converted to a
`PathPrefix` match, and a `parentRef` pointing back at the Gateway:

```yaml
spec:
  parentRefs:
    - name: demo-migration-gateway
      namespace: transfergw
  hostnames:
    - demo.localtest.me
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: sample-nginx
          port: 80
```

The `URLRewrite` filter is the translated form of the Ingress's
`nginx.ingress.kubernetes.io/rewrite-target: /` annotation.

Generated resources carry `transfergw.t-1.dev/managed-by: demo-migration`, which is how
the operator finds them again:

```bash
kubectl get httproute -A -l transfergw.t-1.dev/managed-by=demo-migration
```

### Verify the Gateway actually serves traffic

```bash
kubectl get gateway demo-migration-gateway -n transfergw \
  -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}{"\n"}'

GW_IP=$(kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=demo-migration-gateway \
  -o jsonpath='{.items[0].spec.clusterIP}')

kubectl run curl-gw --rm -it --restart=Never -n demo \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: demo.localtest.me' "http://$GW_IP"
```

A `200` means the generated Gateway path serves the same workload as the original
Ingress. Both are live; cutting real traffic over is a DNS or load-balancer change.

### Reconciliation is continuous

The controller watches Ingresses as well as TransferGWs. Edit the Ingress and the route
follows:

```bash
kubectl patch ingress sample-nginx -n demo --type=json \
  -p='[{"op":"add","path":"/spec/rules/0/http/paths/-","value":{
        "path":"/api","pathType":"Prefix",
        "backend":{"service":{"name":"sample-nginx","port":{"number":80}}}}}]'

kubectl get httproute sample-nginx -n demo -o jsonpath='{.spec.rules[*].matches[*].path.value}{"\n"}'
```

Remove the `migrate=true` label and the generated route is pruned:

```bash
kubectl label ingress sample-nginx -n demo migrate-
kubectl get httproute -n demo
```

---

## Rollout behaviour

`rollout.mode` controls `status.completionPercentage`:

| Mode | Behaviour |
|---|---|
| `immediate` | Jumps to 100% as soon as every selected Ingress converts |
| `canary` / `gradual` | Starts at `canary.initialPercentage` (default 25) and adds `canary.increment` (default 25) every `canary.stepDuration` (default 1h), measured from the resource's creation timestamp |

If any Ingress fails to convert, the percentage is held at 0 rather than advancing over a
partial migration, and the phase becomes `Failed`.

Pause a rollout at any time — the controller returns immediately without touching
resources:

```bash
kubectl patch transfergw demo-migration -n transfergw --type=merge \
  -p '{"spec":{"rollout":{"paused":true}}}'
```

---

## What is and is not implemented

| Capability | Status |
|---|---|
| Ingress discovery by namespace glob, label selector and ingress class | Implemented |
| Gateway generation with HTTP and per-TLS-secret HTTPS listeners | Implemented |
| Ingress to HTTPRoute conversion (hosts, paths, backends) | Implemented |
| Path type mapping, including regex approximation with a warning | Implemented |
| `rewrite-target` translated to a `URLRewrite` filter | Implemented |
| Rate-limit, auth and cert-manager annotations | Reported as issues; no core Gateway API equivalent |
| Pruning routes when an Ingress stops matching | Implemented |
| Status: phase, percentage, processed/resource counts, issues, conditions | Implemented |
| Time-based canary percentage | Implemented |
| Steering real client traffic between Ingress and Gateway | Not implemented — DNS/LB concern |
| Metric-based health monitoring and auto-rollback | Not implemented |
| Admission webhooks | Not implemented |

The `monitoring` and `lifecycle` spec blocks validate and persist, but nothing reads them
yet.

---

## Troubleshooting

**Controller pod stuck in `ImagePullBackOff`**

The GHCR package is private. Make it public, or attach the pull secret from step 5:

```bash
kubectl describe pod -n transfergw -l app=transfergw-controller | tail -20
```

**`no matches for kind "TransferGW"`**

The CRD is not installed. It ships with the chart unless `crd.install=false`:

```bash
kubectl get crd transfergws.transfergw.t-1.dev
```

**No HTTPRoute appears**

Check the operator logs and the resource's status — conversion issues are recorded there
rather than only in the log:

```bash
kubectl describe transfergw demo-migration -n transfergw
kubectl logs -n transfergw deployment/transfergw-controller --tail=100
```

Common causes: the Ingress lacks the `migrate=true` label, its namespace does not match
`selector.namespaces`, its class is not in `selector.ingressClasses`, or the backend
references a Service port by name (HTTPRoute `backendRefs` require a port number).

**HTTPRoute exists but the Gateway rejects it**

The Gateway's `allowedRoutes` must permit the route's namespace. Generated Gateways set
`from: All` because routes live beside their source Ingress. If you supplied your own
Gateway with `generateGateway: false`, widen its `allowedRoutes`.

**ingress-nginx controller pod `Pending`**

The node is missing `ingress-ready=true`. Recreate the cluster with the config from step 1.

**`curl http://demo.localtest.me` connection refused**

The `extraPortMappings` were not applied. Confirm with
`docker port transfergw-control-plane` — you should see 80 and 443 bound.

**Gateway shows no address**

Envoy Gateway has not programmed it. Check:

```bash
kubectl get gateway -n transfergw -o yaml | grep -A10 conditions
kubectl logs -n envoy-gateway-system deployment/envoy-gateway --tail=50
```

---

## Cleanup

```bash
kubectl delete transfergw --all -A
helm uninstall transfergw -n transfergw
helm uninstall eg -n envoy-gateway-system
kind delete cluster --name transfergw
```

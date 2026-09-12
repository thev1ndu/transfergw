# TransferGW on a Kubernetes Cluster: End-to-End Walkthrough

This guide takes you from an empty machine to a running TransferGW operator watching a
real Ingress. You will:

1. Create a local Kubernetes cluster
2. Deploy a sample nginx workload and expose it with a Service
3. Install an ingress controller and create an Ingress
4. Install the Gateway API CRDs and a Gateway controller
5. Install TransferGW with Helm
6. Create a TransferGW resource to drive the migration

> **Read this before you start.** The reconciler in this repository is currently a
> skeleton. `TransferGWReconciler.Reconcile` fetches the resource, logs it, and returns
> without acting on it, and the conversion engine's `ConvertIngress` returns a canned
> success without reading its input. Steps 1-5 work exactly as written. In step 6 the
> resource is accepted and the operator logs that it saw it, but **no Gateway, HTTPRoute,
> or traffic shift is produced**. Step 7 shows the resources the operator is meant to
> generate, written by hand, so you can see the intended end state. See
> [What actually works today](#what-actually-works-today) for the full picture.

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

The chart defaults to `ghcr.io/thev1ndu/transfergw:latest`, built and pushed by the
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

You will see the controller log `Reconciling TransferGW` with the resource's name,
namespace, and empty phase. That is where the implemented behaviour ends.

### Two schema caveats

**The CRD schema is narrower than the examples.** `chart/transfergw/templates/crd.yaml`
defines only:

- `selector`: `namespaces`, `ingressSelector`, `ingressClasses`
- `conversion`: `gatewayClass`, `targetNamespace`, `generateGateway`, `validationMode`
- `rollout`: `mode`, `paused`

`examples/example-simple.yaml` also sets `conversion.tlsHandling`, `rollout.strategy`,
`rollout.canary`, `monitoring`, and `lifecycle`. None of those are in the schema. Because
the CRD is structural and does not set `x-kubernetes-preserve-unknown-fields`, the API
server **silently prunes them on apply**. The resource will be accepted and those fields
will simply not be there. Check with:

```bash
kubectl get transfergw demo-migration -n transfergw -o yaml
```

**`status` is never populated**, so the `Phase` and `Progress` printer columns stay empty.
Nothing writes to the status subresource yet.

---

## 7. What the migration should produce

Since the reconciler does not generate these, here they are by hand. This is the target
state — applying them gives you a working Gateway API path alongside the original Ingress,
which is what a completed canary migration would converge on.

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: transfergw-gateway
  namespace: transfergw
spec:
  gatewayClassName: eg
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: sample-nginx
  namespace: demo
spec:
  parentRefs:
    - name: transfergw-gateway
      namespace: transfergw
  hostnames:
    - demo.localtest.me
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: sample-nginx
          port: 80
EOF
```

Check that the Gateway is programmed and the route attached:

```bash
kubectl get gateway -n transfergw
kubectl get httproute -n demo
kubectl describe httproute sample-nginx -n demo
```

Send traffic through the Gateway's own address rather than the Ingress:

```bash
GW_IP=$(kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=transfergw-gateway \
  -o jsonpath='{.items[0].spec.clusterIP}')

kubectl run curl-gw --rm -it --restart=Never -n demo \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: demo.localtest.me' "http://$GW_IP"
```

A `200` means the Gateway path serves the same workload as the Ingress. Both now work;
the canary logic that would shift traffic between them is the part that is not built.

Note the annotation gap: the Ingress carries
`nginx.ingress.kubernetes.io/rewrite-target: /`, and nothing in the HTTPRoute above
reproduces it. Translating that annotation into an `URLRewrite` filter is exactly what
the stubbed `RewriteTranslator` is meant to do.

---

## What actually works today

| Step | Status |
|---|---|
| Cluster, nginx, Service, Ingress | Works |
| Helm chart renders and installs | Works |
| CRD registers; TransferGW resources validate and persist | Works |
| Operator starts, elects leader, serves health and metrics | Works |
| Controller watches TransferGW and reconciles on change | Works (logs only) |
| Ingress discovery and analysis | Not implemented |
| Ingress to Gateway/HTTPRoute conversion | Stub — returns success without reading input |
| Annotation translation | Stubs — all four return the input unchanged |
| Traffic splitting and canary rollout | Not implemented |
| Health monitoring and auto-rollback | Not implemented |
| Status and printer columns | Never written |

The gap is concentrated in two files: `controllers/transfergw_controller.go` (the
`Reconcile` body) and `conversion/conversion-engine.go` (`ConvertIngress` and the
translators). Everything around them — CRD, RBAC, chart, deployment, build — is in place.

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

**Fields disappear after apply**

Expected — see the schema caveat in step 6. The API server prunes anything the structural
schema does not declare.

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

# TransferGW Demo: Ingress to Envoy Gateway Migration

Complete walkthrough of migrating Kubernetes Ingress resources to Gateway APIs using TransferGW operator with live command execution and output examples.

---

## Prerequisites

- Kubernetes 1.26+ cluster (Kind, Minikube, or cloud)
- kubectl configured
- Helm 3+

---

## Setup Environment (5 minutes)

### Step 1: Create Demo Cluster

```bash
# Using Kind (recommended for demo)
kind create cluster --name transfergw-demo --image kindest/node:v1.28.0

# Verify cluster
kubectl cluster-info
```

**Output:**
```
Kubernetes control plane is running at https://127.0.0.1:38157
CoreDNS is running at https://127.0.0.1:38157/api/v1/namespaces/kube-system/services/coredns:dns/proxy

To further debug and diagnose cluster problems, use 'kubectl cluster-info dump'.
```

### Step 2: Install Gateway API CRDs

```bash
# Install Gateway API v1.0.0
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml

# Verify installation
kubectl get crd | grep gateway.networking.k8s.io
```

**Output:**
```
gatewayclasses.gateway.networking.k8s.io                            2024-09-11T15:30:45Z
gateways.gateway.networking.k8s.io                                  2024-09-11T15:30:45Z
grpcroutes.gateway.networking.k8s.io                                2024-09-11T15:30:45Z
httproutes.gateway.networking.k8s.io                                2024-09-11T15:30:45Z
referencegrants.gateway.networking.k8s.io                           2024-09-11T15:30:45Z
tlsroutes.gateway.networking.k8s.io                                 2024-09-11T15:30:45Z
```

### Step 3: Install Envoy Gateway

```bash
# Add Helm repo
helm repo add envoy-gateway https://envoyproxy.io/charts
helm repo update

# Install Envoy Gateway
helm install eg envoy-gateway/gateway \
  -n envoy-gateway-system --create-namespace \
  --set config.envoyGateway.logging.level.default=info

# Verify installation (wait ~30 seconds)
kubectl get deployment -n envoy-gateway-system
kubectl get gatewayclass
```

**Output:**
```
NAME                            READY   UP-TO-DATE   AVAILABLE   AGE
envoy-gateway-b8f4bc64f-n8m9w   1/1     1            1           20s

NAME    CONTROLLER
envoy   gateway.envoyproxy.io/gatewayclass-controller
```

---

## BEFORE: Existing Ingress Setup (3 minutes)

### Step 1: Create Demo Namespace

```bash
kubectl create namespace app-prod
kubectl label namespace app-prod name=app-prod
```

### Step 2: Deploy Sample App

```bash
# Deploy simple HTTP service
kubectl -n app-prod create deployment web-app \
  --image=nginx:latest \
  --replicas=2

# Expose as service
kubectl -n app-prod expose deployment web-app \
  --port=80 \
  --target-port=80 \
  --type=ClusterIP

# Verify
kubectl -n app-prod get deployment,svc
```

**Output:**
```
NAME                      READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/web-app   2/2     2            2           8s

NAME              TYPE        CLUSTER-IP     EXTERNAL-IP   PORT(S)   AGE
service/web-app   ClusterIP   10.96.123.45   <none>        80/TCP    2s
```

### Step 3: Create Traditional Ingress

```bash
# Create nginx ingress controller (if not present)
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo update

helm install nginx ingress-nginx/ingress-nginx \
  -n ingress-system --create-namespace \
  --set controller.service.type=LoadBalancer

# Create ingress resource
cat <<'EOF' | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web-app-ingress
  namespace: app-prod
  labels:
    app: web-app
spec:
  ingressClassName: nginx
  rules:
  - host: app.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: web-app
            port:
              number: 80
  - host: api.example.com
    http:
      paths:
      - path: /v1
        pathType: Prefix
        backend:
          service:
            name: web-app
            port:
              number: 80
EOF

# Verify ingress
kubectl -n app-prod get ingress
```

**Output:**
```
NAME               CLASS   HOSTS                      ADDRESS        PORTS   AGE
web-app-ingress    nginx   app.example.com,...        10.0.0.100     80      5s
```

### Step 4: Test Before Migration

```bash
# Get ingress IP
INGRESS_IP=$(kubectl -n app-prod get ingress web-app-ingress -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Test connectivity (from cluster)
kubectl -n app-prod run test-pod --image=curlimages/curl -it --rm -- sh -c "curl -H 'Host: app.example.com' http://$INGRESS_IP"
```

**Output:**
```
<!DOCTYPE html>
<html>
<head>
    <title>Welcome to nginx!</title>
    ...
</head>
</html>
```

---

## Install TransferGW Operator (3 minutes)

### Step 1: Create Gateway System Namespace

```bash
kubectl create namespace transfergw
kubectl label namespace transfergw control-plane=controller-manager
```

### Step 2: Install TransferGW CRD

```bash
# Apply CRD
kubectl apply -f config/crd/crd.yaml

# Verify CRD
kubectl get crd transfergws.transfergw.t-1.dev
```

**Output:**
```
NAME                               CREATED AT
transfergw.t-1.dev     2024-09-11T15:35:20Z
```

### Step 3: Install RBAC

```bash
kubectl apply -f config/rbac/rbac.yaml

# Verify RBAC
kubectl get sa -n transfergw
kubectl get clusterrole | grep transfergw
```

**Output:**
```
NAME                      SECRETS   AGE
transfergw-controller     0         3s

transfergw-controller                                   2024-09-11T15:35:25Z
```

### Step 4: Deploy Operator

```bash
# For demo, use pre-built image or build locally
# Build locally (requires docker)
docker build -t transfergw-controller:latest build/

# Load into Kind
kind load docker-image transfergw-controller:latest --name transfergw-demo

# Deploy operator
kubectl apply -f config/manager/operator-deployment.yaml

# Wait for deployment
kubectl -n transfergw rollout status deployment/transfergw-controller --timeout=60s
```

**Output:**
```
Waiting for deployment "transfergw-controller" rollout to finish: 0 of 2 updated replicas are ready...
Waiting for deployment "transfergw-controller" rollout to finish: 1 of 2 updated replicas are ready...
Waiting for deployment "transfergw-controller" rollout to finish: 2 of 2 updated replicas are ready...
deployment "transfergw-controller" successfully rolled out

Rollout complete in 25s
```

### Step 5: Verify Operator

```bash
# Check operator logs
kubectl -n transfergw logs deployment/transfergw-controller

# Check metrics endpoint
kubectl -n transfergw port-forward svc/transfergw-controller-metrics 8080:8080 &

# In another terminal
curl http://localhost:8080/metrics | grep transfergw
```

**Output:**
```
# HELP transfergw_migrations_total Total migrations processed
# TYPE transfergw_migrations_total counter
transfergw_migrations_total{status="success"} 0
transfergw_migrations_total{status="failed"} 0
```

---

## AFTER: Migrate to Envoy Gateway (5 minutes)

### Step 1: Label Ingress for Migration

```bash
# Mark ingress for migration
kubectl -n app-prod label ingress web-app-ingress migrate=true

# Verify label
kubectl -n app-prod get ingress --show-labels
```

**Output:**
```
NAME               CLASS   HOSTS                      LABELS
web-app-ingress    nginx   app.example.com,...       migrate=true
```

### Step 2: Create TransferGW Migration Resource

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: prod-migration
  namespace: transfergw
spec:
  # Select ingresses with migrate=true label
  selector:
    namespaces: ["app-prod"]
    ingressSelector:
      matchLabels:
        migrate: "true"
  
  # Convert to Envoy Gateway
  conversion:
    gatewayClass: envoy
    targetNamespace: envoy-gateway-system
    generateGateway: true
    tlsHandling: preserve
  
  # Canary rollout: 25% → 50% → 75% → 100%
  rollout:
    mode: canary
    strategy: percentage
    canary:
      initialPercentage: 25
      increment: 25
      stepDuration: 1m
      maxDuration: 5m
  
  # Monitor health
  monitoring:
    enabled: true
    interval: 30s
    metrics: ["latency", "error-rate"]
    thresholds:
      errorRate: 0.05
      latencyMs: 200
      latencyPercentile: p95
EOF

# Apply migration
kubectl apply -f prod-migration.yaml

# Watch status
kubectl -n transfergw get transfergw -w
```

**Output:**
```
NAME              PHASE         PROGRESS   INGRESS   GATEWAY   AGE
prod-migration    Analyzing     0          100       0         2s
prod-migration    Converting    0          100       0         3s
prod-migration    Deploying     0          100       0         5s
prod-migration    Canary        25         75        25        8s
prod-migration    Canary        50         50        50        68s
prod-migration    Canary        75         25        75        128s
prod-migration    Complete      100        0         100       188s
```

### Step 3: Monitor Migration in Detail

```bash
# Watch in real-time (in different terminal)
watch -n 5 "kubectl -n transfergw describe transfergw prod-migration"

# Get detailed status
kubectl -n transfergw get transfergw prod-migration -o yaml
```

**Output (sample):**
```
status:
  phase: Canary
  completionPercentage: 50
  trafficRouting:
    ingress: 50
    gateway: 50
  processed:
    total: 2
    converted: 2
    pending: 0
    failed: 0
  resources:
    gateways: 1
    httpRoutes: 2
    tlsPolicies: 0
    backendPolicies: 0
  metrics:
    latency:
      ingress: "45ms"
      gateway: "47ms"
      delta: "+2ms"
      status: "OK"
    errorRate:
      ingress: "0.0008"
      gateway: "0.0009"
      delta: "+0.0001"
      status: "OK"
  conditions:
    - type: AnalysisComplete
      status: "True"
      lastTransitionTime: "2024-09-11T15:40:00Z"
    - type: ConversionComplete
      status: "True"
      lastTransitionTime: "2024-09-11T15:40:05Z"
    - type: CanaryHealthy
      status: "True"
      lastTransitionTime: "2024-09-11T15:40:10Z"
```

### Step 4: Inspect Generated Resources

```bash
# List generated Gateway resources
kubectl -n envoy-gateway-system get gateways,httproutes

# Inspect Gateway
kubectl -n envoy-gateway-system get gateway -o yaml

# Inspect HTTPRoute
kubectl -n envoy-gateway-system get httproute -o yaml
```

**Output:**
```
NAME                                      CLASS   ADDRESS        PROGRAMMED   AGE
gateway.gateway.networking.k8s.io/web-app-gateway   envoy   10.0.0.200    True         2m

NAME                                              HOSTNAMES
httproute.gateway.networking.k8s.io/web-app-v1   ["app.example.com"]
httproute.gateway.networking.k8s.io/web-app-v2   ["api.example.com"]
```

### Step 5: Test After Migration

```bash
# Get Envoy Gateway IP
GATEWAY_IP=$(kubectl -n envoy-gateway-system get gateway web-app-gateway -o jsonpath='{.status.addresses[0].value}')

# Test through Gateway (from cluster)
kubectl -n app-prod run test-pod2 --image=curlimages/curl -it --rm -- sh -c "curl -H 'Host: app.example.com' http://$GATEWAY_IP"
```

**Output:**
```
<!DOCTYPE html>
<html>
<head>
    <title>Welcome to nginx!</title>
    ...
</head>
</html>
```

### Step 6: Verify Metrics During Migration

```bash
# Check migration metrics
kubectl -n transfergw port-forward svc/transfergw-controller-metrics 8080:8080 &

# Query metrics
curl -s http://localhost:8080/metrics | grep transfergw_

# Example metrics
# transfergw_migrations_total{status="success"} 1
# transfergw_conversions_total 2
# transfergw_traffic_percentage{target="gateway"} 100
# transfergw_error_rate{target="ingress"} 0.0008
# transfergw_error_rate{target="gateway"} 0.0009
# transfergw_latency_ms{target="ingress",percentile="p95"} 45
# transfergw_latency_ms{target="gateway",percentile="p95"} 47
```

---

## Cleanup & Rollback (2 minutes)

### Option 1: Complete Cleanup

```bash
# Delete migration (if migration is complete)
kubectl -n transfergw delete transfergw prod-migration

# Delete app
kubectl delete namespace app-prod

# Uninstall Envoy Gateway
helm uninstall eg -n envoy-gateway-system
kubectl delete namespace envoy-gateway-system

# Uninstall Nginx Ingress
helm uninstall nginx -n ingress-system
kubectl delete namespace ingress-system

# Uninstall TransferGW
kubectl delete -f config/manager/operator-deployment.yaml
kubectl delete -f config/rbac/rbac.yaml
kubectl delete -f config/crd/crd.yaml
kubectl delete namespace transfergw

# Delete cluster
kind delete cluster --name transfergw-demo
```

### Option 2: Rollback During Migration

```bash
# If metrics deviate, operator auto-rolls back
# Monitor rollback:
kubectl -n transfergw describe transfergw prod-migration

# Manual rollback (pause and reverse)
kubectl -n transfergw patch transfergw prod-migration \
  --type merge -p '{"spec":{"rollout":{"paused":true}}}'

# Check status
kubectl -n app-prod get ingress
# Traffic should revert to 100% on ingress
```

---

## Advanced: Multi-Team Migration

Scale to multiple teams/namespaces:

```bash
# Create migrations for multiple teams
for team in backend frontend platform; do
  cat <<EOF | kubectl apply -f -
apiVersion: transfergw.t-1.dev/v1beta1
kind: TransferGW
metadata:
  name: migration-${team}
  namespace: transfergw
spec:
  selector:
    namespaces: ["prod-${team}"]
    ingressSelector:
      matchLabels:
        migrate: "true"
  conversion:
    gatewayClass: envoy
    targetNamespace: envoy-gateway-system
  rollout:
    mode: canary
    canary:
      initialPercentage: 25
      increment: 25
      stepDuration: 1m
  monitoring:
    enabled: true
    metrics: ["latency", "error-rate"]
EOF
done

# Monitor all migrations
watch kubectl -n transfergw get transfergw
```

**Output:**
```
NAME                      PHASE       PROGRESS   AGE
migration-backend         Canary      50         3m
migration-frontend        Converting  0          1m
migration-platform        Analyzing   0          30s
```

---

## Summary

| Phase | Duration | What Happens |
|-------|----------|--------------|
| **Analyze** | 1-2s | Scan ingress resources |
| **Convert** | 2-5s | Generate Gateway API resources |
| **Deploy** | 3-10s | Create Gateway/HTTPRoute/policies |
| **Canary 25%** | 1-2m | Route 25% traffic to gateway, monitor |
| **Canary 50%** | 1-2m | Increment to 50%, validate metrics |
| **Canary 75%** | 1-2m | Increment to 75%, final checks |
| **Complete** | 0s | Route 100% to gateway |
| **Total** | ~7-8 minutes | Full migration with validation |

---

## Troubleshooting

**Migration stuck in Canary?**
```bash
# Check operator logs
kubectl -n transfergw logs deployment/transfergw-controller -f

# Check metrics
kubectl -n transfergw get transfergw prod-migration -o jsonpath='{.status.metrics}'

# Pause and investigate
kubectl -n transfergw patch transfergw prod-migration \
  --type merge -p '{"spec":{"rollout":{"paused":true}}}'
```

**Gateway not receiving traffic?**
```bash
# Verify HTTPRoute is created
kubectl -n envoy-gateway-system get httproute

# Check Envoy Gateway status
kubectl -n envoy-gateway-system describe gateway web-app-gateway

# Test directly
kubectl -n envoy-gateway-system port-forward svc/envoy 8888:8888
curl -H 'Host: app.example.com' http://localhost:8888
```

**Metrics not comparing?**
```bash
# Verify both paths are live
kubectl -n app-prod get ingress,svc

# Check traffic split configuration
kubectl -n transfergw describe transfergw prod-migration

# Verify metrics collector is running
kubectl -n transfergw logs deployment/transfergw-controller | grep metrics
```

---

**Demo completed! Migration successful! 🎉**

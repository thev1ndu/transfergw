// Copyright 2026.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build e2e

// Package e2e exercises TransferGW against a real, running cluster: a real
// API server, real CRDs, real RBAC, and the actual controller binary/image -
// none of which internal/controller's fake-client unit tests can catch
// (a missing RBAC verb, a CRD that failed to install, the controller
// crash-looping on startup).
//
// It assumes the cluster already has the Gateway API CRDs and this
// project's own CRD+controller installed - see `make test-e2e`, which
// chains kind-create, kind-load, gatewayapi-crds and deploy before running
// this suite, in that order.
package e2e

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
)

const (
	demoNamespace  = "e2e-demo"
	gatewayClass   = "e2e-gatewayclass"
	waitTimeout    = 2 * time.Minute
	waitPollPeriod = 2 * time.Second
)

func newTestClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("adding client-go scheme: %v", err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatalf("adding gateway-api scheme: %v", err)
	}
	if err := transfergwv1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding transfergw scheme: %v", err)
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Skipf("no kubeconfig / in-cluster config available, skipping e2e suite: %v", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	// Fail fast and with a clear message if the Gateway API CRDs aren't
	// installed, rather than have every later step time out mysteriously.
	if err := c.List(context.Background(), &gatewayv1.GatewayClassList{}); err != nil {
		t.Fatalf("listing GatewayClasses (are the Gateway API CRDs installed? see `make gatewayapi-crds`): %v", err)
	}
	return c
}

// TestMigratesASampleIngressToAnHTTPRoute drives one full migration against
// the live cluster: a Deployment+Service+Ingress, a GatewayClass, and a
// TransferGW selecting them, then asserts the controller actually produced
// a Gateway and an HTTPRoute for it.
func TestMigratesASampleIngressToAnHTTPRoute(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: demoNamespace}}
	if err := c.Create(ctx, ns); err != nil {
		t.Fatalf("creating namespace %s: %v", demoNamespace, err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), ns)
	})

	gc := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: gatewayClass},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: "example.com/e2e-test-controller"},
	}
	if err := c.Create(ctx, gc); err != nil {
		t.Fatalf("creating gatewayclass: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), gc)
	})

	deployDemoWorkload(ctx, t, c)

	migration := &transfergwv1beta1.TransferGW{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-migration", Namespace: demoNamespace},
		Spec: transfergwv1beta1.TransferGWSpec{
			Selector: transfergwv1beta1.SelectorSpec{
				Namespaces:      []string{demoNamespace},
				IngressSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"migrate": "true"}},
			},
			Conversion: transfergwv1beta1.ConversionSpec{
				GatewayClass:    gatewayClass,
				GenerateGateway: true,
			},
			Rollout: transfergwv1beta1.RolloutSpec{Mode: "immediate"},
		},
	}
	if err := c.Create(ctx, migration); err != nil {
		t.Fatalf("creating TransferGW: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), migration)
	})

	waitFor(t, "an HTTPRoute for the sample Ingress", func() (bool, error) {
		var routes gatewayv1.HTTPRouteList
		if err := c.List(ctx, &routes, client.InNamespace(demoNamespace)); err != nil {
			return false, err
		}
		return len(routes.Items) == 1, nil
	})

	waitFor(t, "the TransferGW to report Converted >= 1", func() (bool, error) {
		got := &transfergwv1beta1.TransferGW{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(migration), got); err != nil {
			return false, err
		}
		return got.Status.ProcessedStatus != nil && got.Status.ProcessedStatus.Converted >= 1, nil
	})
}

func deployDemoWorkload(ctx context.Context, t *testing.T, c client.Client) {
	t.Helper()

	labels := map[string]string{"app": "e2e-demo"}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-demo", Namespace: demoNamespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: intPtr(1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "web",
						Image: "registry.k8s.io/e2e-test-images/agnhost:2.45",
						Args:  []string{"netexec", "--http-port=8080"},
						Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
					}},
				},
			},
		},
	}
	if err := c.Create(ctx, deploy); err != nil {
		t.Fatalf("creating deployment: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), deploy) })

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-demo", Namespace: demoNamespace},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(8080)}},
		},
	}
	if err := c.Create(ctx, svc); err != nil {
		t.Fatalf("creating service: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), svc) })

	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-demo",
			Namespace: demoNamespace,
			Labels:    map[string]string{"migrate": "true"},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: "e2e-demo.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "e2e-demo",
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if err := c.Create(ctx, ing); err != nil {
		t.Fatalf("creating ingress: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), ing) })
}

// waitFor polls check until it returns true, or fails the test after
// waitTimeout - long enough for the controller's own requeue interval to
// fire a few times against a real cluster, without hanging CI forever if
// something is actually broken.
func waitFor(t *testing.T, what string, check func() (bool, error)) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), waitPollPeriod, waitTimeout, true,
		func(context.Context) (bool, error) { return check() })
	if err != nil {
		t.Fatalf("waiting for %s: %v", what, err)
	}
}

func intPtr(i int32) *int32 { return &i }

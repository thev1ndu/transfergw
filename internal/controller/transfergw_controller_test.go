package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/conversion"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		corev1.AddToScheme,
		networkingv1.AddToScheme,
		gatewayv1.Install,
		transfergwv1beta1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("building scheme: %v", err)
		}
	}
	return s
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
}

func testIngress(name, ns string, labels map[string]string) *networkingv1.Ingress {
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To("nginx"),
			Rules: []networkingv1.IngressRule{{
				Host: name + ".localtest.me",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: ptr.To(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: name,
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
}

func testMigration(mutators ...func(*transfergwv1beta1.TransferGW)) *transfergwv1beta1.TransferGW {
	m := &transfergwv1beta1.TransferGW{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "demo-migration",
			Namespace:         "transfergw",
			CreationTimestamp: metav1.Now(),
		},
		Spec: transfergwv1beta1.TransferGWSpec{
			Selector: transfergwv1beta1.SelectorSpec{
				Namespaces:      []string{"demo"},
				IngressSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"migrate": "true"}},
				IngressClasses:  []string{"nginx"},
			},
			Conversion: transfergwv1beta1.ConversionSpec{
				GatewayClass:    "eg",
				TargetNamespace: "transfergw",
				GenerateGateway: true,
			},
			Rollout: transfergwv1beta1.RolloutSpec{Mode: "immediate"},
		},
	}
	for _, mut := range mutators {
		mut(m)
	}
	return m
}

func newReconciler(t *testing.T, objs ...client.Object) (*TransferGWReconciler, client.Client) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&transfergwv1beta1.TransferGW{}).
		Build()

	return &TransferGWReconciler{
		Client:           c,
		Scheme:           s,
		ConversionEngine: conversion.NewEngine(),
	}, c
}

func reconcileOnce(t *testing.T, r *TransferGWReconciler, m *transfergwv1beta1.TransferGW) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: m.Name, Namespace: m.Namespace},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	return res
}

func TestReconcileGeneratesGatewayAndRoute(t *testing.T) {
	migration := testMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	// The Gateway lands in the target namespace.
	gw := &gatewayv1.Gateway{}
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "demo-migration-gateway", Namespace: "transfergw"}, gw); err != nil {
		t.Fatalf("expected a Gateway: %v", err)
	}
	if gw.Spec.GatewayClassName != "eg" {
		t.Errorf("gatewayClassName = %q, want eg", gw.Spec.GatewayClassName)
	}
	if len(gw.Spec.Listeners) != 1 || gw.Spec.Listeners[0].Port != 80 {
		t.Errorf("expected one HTTP listener on port 80, got %+v", gw.Spec.Listeners)
	}

	// The HTTPRoute lands beside its source Ingress, not in the target namespace.
	route := &gatewayv1.HTTPRoute{}
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "sample-nginx", Namespace: "demo"}, route); err != nil {
		t.Fatalf("expected an HTTPRoute in namespace demo: %v", err)
	}
	if route.Labels[managedByLabel] != "demo-migration" {
		t.Errorf("managed-by label = %q, want demo-migration", route.Labels[managedByLabel])
	}
	if got := route.Spec.ParentRefs[0].Name; got != "demo-migration-gateway" {
		t.Errorf("parentRef = %q, want demo-migration-gateway", got)
	}

	// Status reflects a completed immediate rollout.
	got := &transfergwv1beta1.TransferGW{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(migration), got); err != nil {
		t.Fatalf("re-reading migration: %v", err)
	}
	if got.Status.Phase != phaseComplete {
		t.Errorf("phase = %q, want %q", got.Status.Phase, phaseComplete)
	}
	if got.Status.CompletionPercentage != 100 {
		t.Errorf("completionPercentage = %d, want 100", got.Status.CompletionPercentage)
	}
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Converted != 1 {
		t.Errorf("processed = %+v, want 1 converted", got.Status.ProcessedStatus)
	}
	if got.Status.ResourcesStatus == nil || got.Status.ResourcesStatus.HTTPRoutes != 1 {
		t.Errorf("resources = %+v, want 1 HTTPRoute", got.Status.ResourcesStatus)
	}
}

func TestReconcileSkipsUnlabelledIngress(t *testing.T) {
	migration := testMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("no-label", "demo", nil),
		migration,
	)

	reconcileOnce(t, r, migration)

	routes := &gatewayv1.HTTPRouteList{}
	if err := c.List(context.Background(), routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 0 {
		t.Errorf("routes = %d, want 0 for an unlabelled Ingress", len(routes.Items))
	}
}

func TestReconcileIgnoresOtherNamespaces(t *testing.T) {
	migration := testMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("other"), namespace("transfergw"),
		testIngress("elsewhere", "other", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	routes := &gatewayv1.HTTPRouteList{}
	if err := c.List(context.Background(), routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 0 {
		t.Errorf("routes = %d, want 0 outside the selected namespace", len(routes.Items))
	}
}

func TestReconcileNamespaceGlob(t *testing.T) {
	migration := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Spec.Selector.Namespaces = []string{"prod-*"}
	})
	r, c := newReconciler(t,
		namespace("prod-a"), namespace("prod-b"), namespace("staging"), namespace("transfergw"),
		testIngress("a", "prod-a", map[string]string{"migrate": "true"}),
		testIngress("b", "prod-b", map[string]string{"migrate": "true"}),
		testIngress("c", "staging", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	routes := &gatewayv1.HTTPRouteList{}
	if err := c.List(context.Background(), routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 2 {
		t.Errorf("routes = %d, want 2 matching prod-*", len(routes.Items))
	}
}

func TestReconcileFiltersByIngressClass(t *testing.T) {
	migration := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Spec.Selector.IngressClasses = []string{"traefik"}
	})
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	routes := &gatewayv1.HTTPRouteList{}
	if err := c.List(context.Background(), routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 0 {
		t.Errorf("routes = %d, want 0 for a non-matching ingress class", len(routes.Items))
	}
}

func TestReconcilePausedIsNoOp(t *testing.T) {
	migration := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Spec.Rollout.Paused = true
	})
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	routes := &gatewayv1.HTTPRouteList{}
	if err := c.List(context.Background(), routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 0 {
		t.Errorf("routes = %d, want 0 while paused", len(routes.Items))
	}
}

func TestReconcileSkipsGatewayWhenNotRequested(t *testing.T) {
	migration := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Spec.Conversion.GenerateGateway = false
	})
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	gateways := &gatewayv1.GatewayList{}
	if err := c.List(context.Background(), gateways); err != nil {
		t.Fatalf("listing gateways: %v", err)
	}
	if len(gateways.Items) != 0 {
		t.Errorf("gateways = %d, want 0 when generateGateway is false", len(gateways.Items))
	}

	// The route is still generated and still points at the expected Gateway name.
	route := &gatewayv1.HTTPRoute{}
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "sample-nginx", Namespace: "demo"}, route); err != nil {
		t.Fatalf("expected an HTTPRoute: %v", err)
	}
}

func TestReconcilePrunesRouteWhenIngressStopsMatching(t *testing.T) {
	migration := testMigration()
	ing := testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"})
	r, c := newReconciler(t, namespace("demo"), namespace("transfergw"), ing, migration)

	reconcileOnce(t, r, migration)

	route := &gatewayv1.HTTPRoute{}
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "sample-nginx", Namespace: "demo"}, route); err != nil {
		t.Fatalf("expected an HTTPRoute after the first reconcile: %v", err)
	}

	// Drop the label so the Ingress no longer matches, then reconcile again.
	live := &networkingv1.Ingress{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(ing), live); err != nil {
		t.Fatalf("re-reading ingress: %v", err)
	}
	live.Labels = map[string]string{}
	if err := c.Update(context.Background(), live); err != nil {
		t.Fatalf("updating ingress: %v", err)
	}

	reconcileOnce(t, r, migration)

	err := c.Get(context.Background(),
		types.NamespacedName{Name: "sample-nginx", Namespace: "demo"}, &gatewayv1.HTTPRoute{})
	if err == nil {
		t.Error("expected the orphaned HTTPRoute to be pruned")
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	migration := testMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	first := &gatewayv1.HTTPRoute{}
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "sample-nginx", Namespace: "demo"}, first); err != nil {
		t.Fatalf("first reconcile produced no route: %v", err)
	}

	reconcileOnce(t, r, migration)

	second := &gatewayv1.HTTPRoute{}
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: "sample-nginx", Namespace: "demo"}, second); err != nil {
		t.Fatalf("second reconcile lost the route: %v", err)
	}

	if first.ResourceVersion != second.ResourceVersion {
		t.Errorf("route was rewritten on an unchanged reconcile: %s -> %s",
			first.ResourceVersion, second.ResourceVersion)
	}
}

func TestReconcileNoMatchingIngressesStaysAnalyzing(t *testing.T) {
	migration := testMigration()
	r, c := newReconciler(t, namespace("demo"), namespace("transfergw"), migration)

	reconcileOnce(t, r, migration)

	got := &transfergwv1beta1.TransferGW{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(migration), got); err != nil {
		t.Fatalf("re-reading migration: %v", err)
	}
	if got.Status.Phase != phaseAnalyzing {
		t.Errorf("phase = %q, want %q", got.Status.Phase, phaseAnalyzing)
	}
	if got.Status.CompletionPercentage != 0 {
		t.Errorf("completionPercentage = %d, want 0", got.Status.CompletionPercentage)
	}
}

func TestReconcileFailedConversionReportsFailure(t *testing.T) {
	migration := testMigration()
	// A named backend port cannot be expressed as an HTTPRoute backendRef.
	bad := testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"})
	bad.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port = networkingv1.ServiceBackendPort{Name: "http"}

	r, c := newReconciler(t, namespace("demo"), namespace("transfergw"), bad, migration)
	reconcileOnce(t, r, migration)

	got := &transfergwv1beta1.TransferGW{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(migration), got); err != nil {
		t.Fatalf("re-reading migration: %v", err)
	}
	if got.Status.Phase != phaseFailed {
		t.Errorf("phase = %q, want %q", got.Status.Phase, phaseFailed)
	}
	if len(got.Status.Issues) == 0 {
		t.Error("expected conversion issues on status")
	}
}

func TestRolloutPercentage(t *testing.T) {
	immediate := testMigration()
	if got := rolloutPercentage(immediate, 1, 0); got != 100 {
		t.Errorf("immediate rollout = %d, want 100", got)
	}
	if got := rolloutPercentage(immediate, 0, 0); got != 0 {
		t.Errorf("nothing converted = %d, want 0", got)
	}
	if got := rolloutPercentage(immediate, 1, 1); got != 0 {
		t.Errorf("partial failure should hold at 0, got %d", got)
	}

	canary := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Spec.Rollout.Mode = "canary"
	})
	// Freshly created, so no step has elapsed yet: the initial percentage.
	if got := rolloutPercentage(canary, 1, 0); got != 25 {
		t.Errorf("fresh canary = %d, want the 25%% initial share", got)
	}
}

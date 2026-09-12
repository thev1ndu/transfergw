package conversion

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ingress builds a minimal single-path Ingress for tests.
func ingress(mutators ...func(*networkingv1.Ingress)) *networkingv1.Ingress {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "sample-nginx", Namespace: "demo"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To("nginx"),
			Rules: []networkingv1.IngressRule{{
				Host: "demo.localtest.me",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: ptr.To(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "sample-nginx",
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
	for _, m := range mutators {
		m(ing)
	}
	return ing
}

func defaultOpts() Options {
	return Options{GatewayName: "demo-gateway", GatewayNamespace: "transfergw"}
}

func TestConvertIngressBasic(t *testing.T) {
	res := NewEngine().ConvertIngress(ingress(), defaultOpts())

	if res.Route == nil {
		t.Fatalf("expected a route, got nil (issues: %+v)", res.Issues)
	}
	if res.Failed() {
		t.Fatalf("unexpected failure: %+v", res.Issues)
	}

	route := res.Route
	if route.Name != "sample-nginx" || route.Namespace != "demo" {
		t.Errorf("route identity = %s/%s, want demo/sample-nginx", route.Namespace, route.Name)
	}

	if got := len(route.Spec.Hostnames); got != 1 {
		t.Fatalf("hostnames = %d, want 1", got)
	}
	if route.Spec.Hostnames[0] != "demo.localtest.me" {
		t.Errorf("hostname = %q, want demo.localtest.me", route.Spec.Hostnames[0])
	}

	if got := len(route.Spec.ParentRefs); got != 1 {
		t.Fatalf("parentRefs = %d, want 1", got)
	}
	parent := route.Spec.ParentRefs[0]
	if parent.Name != "demo-gateway" {
		t.Errorf("parent name = %q, want demo-gateway", parent.Name)
	}
	// Gateway is in a different namespace, so the ref must be qualified.
	if parent.Namespace == nil || *parent.Namespace != "transfergw" {
		t.Errorf("parent namespace = %v, want transfergw", parent.Namespace)
	}

	if got := len(route.Spec.Rules); got != 1 {
		t.Fatalf("rules = %d, want 1", got)
	}
	rule := route.Spec.Rules[0]

	if got := *rule.Matches[0].Path.Type; got != gatewayv1.PathMatchPathPrefix {
		t.Errorf("path type = %v, want PathPrefix", got)
	}
	if got := *rule.Matches[0].Path.Value; got != "/" {
		t.Errorf("path value = %q, want /", got)
	}

	backend := rule.BackendRefs[0]
	if backend.Name != "sample-nginx" {
		t.Errorf("backend name = %q, want sample-nginx", backend.Name)
	}
	if backend.Port == nil || *backend.Port != 80 {
		t.Errorf("backend port = %v, want 80", backend.Port)
	}
}

func TestConvertIngressSameNamespaceParentIsUnqualified(t *testing.T) {
	opts := defaultOpts()
	opts.GatewayNamespace = "demo" // same as the Ingress

	res := NewEngine().ConvertIngress(ingress(), opts)
	if res.Route == nil {
		t.Fatal("expected a route")
	}
	if ns := res.Route.Spec.ParentRefs[0].Namespace; ns != nil {
		t.Errorf("parent namespace = %v, want nil for a same-namespace Gateway", *ns)
	}
}

func TestConvertPathTypes(t *testing.T) {
	tests := []struct {
		name     string
		pathType networkingv1.PathType
		path     string
		want     gatewayv1.PathMatchType
		wantVal  string
	}{
		{"exact", networkingv1.PathTypeExact, "/healthz", gatewayv1.PathMatchExact, "/healthz"},
		{"prefix", networkingv1.PathTypePrefix, "/api", gatewayv1.PathMatchPathPrefix, "/api"},
		{"implementation specific", networkingv1.PathTypeImplementationSpecific, "/api", gatewayv1.PathMatchPathPrefix, "/api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ing := ingress(func(i *networkingv1.Ingress) {
				i.Spec.Rules[0].HTTP.Paths[0].PathType = ptr.To(tt.pathType)
				i.Spec.Rules[0].HTTP.Paths[0].Path = tt.path
			})

			res := NewEngine().ConvertIngress(ing, defaultOpts())
			if res.Route == nil {
				t.Fatalf("expected a route, issues: %+v", res.Issues)
			}

			match := res.Route.Spec.Rules[0].Matches[0].Path
			if *match.Type != tt.want {
				t.Errorf("path type = %v, want %v", *match.Type, tt.want)
			}
			if *match.Value != tt.wantVal {
				t.Errorf("path value = %q, want %q", *match.Value, tt.wantVal)
			}
		})
	}
}

func TestConvertRegexPathWarns(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Spec.Rules[0].HTTP.Paths[0].PathType = ptr.To(networkingv1.PathTypeImplementationSpecific)
		i.Spec.Rules[0].HTTP.Paths[0].Path = "/api/v1/.*"
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route == nil {
		t.Fatal("expected a route despite the regex path")
	}
	if res.Failed() {
		t.Errorf("regex path should warn, not fail: %+v", res.Issues)
	}

	if got := *res.Route.Spec.Rules[0].Matches[0].Path.Value; got != "/api/v1" {
		t.Errorf("approximated prefix = %q, want /api/v1", got)
	}

	if !hasSeverity(res.Issues, SeverityWarning) {
		t.Errorf("expected a warning issue, got %+v", res.Issues)
	}
}

func TestRewriteAnnotationBecomesFilter(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/rewrite-target": "/",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route == nil {
		t.Fatalf("expected a route, issues: %+v", res.Issues)
	}

	filters := res.Route.Spec.Rules[0].Filters
	if len(filters) != 1 {
		t.Fatalf("filters = %d, want 1", len(filters))
	}
	if filters[0].Type != gatewayv1.HTTPRouteFilterURLRewrite {
		t.Errorf("filter type = %v, want URLRewrite", filters[0].Type)
	}
	if got := *filters[0].URLRewrite.Path.ReplacePrefixMatch; got != "/" {
		t.Errorf("replacePrefixMatch = %q, want /", got)
	}
}

func TestRewriteWithCaptureGroupWarnsAndEmitsNoFilter(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/rewrite-target": "/$1",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route == nil {
		t.Fatal("expected a route")
	}
	if got := len(res.Route.Spec.Rules[0].Filters); got != 0 {
		t.Errorf("filters = %d, want 0 for an inexpressible rewrite", got)
	}
	if !hasSeverity(res.Issues, SeverityWarning) {
		t.Errorf("expected a warning, got %+v", res.Issues)
	}
}

func TestUntranslatableAnnotationsWarn(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/limit-rps":  "100",
			"nginx.ingress.kubernetes.io/auth-type":  "basic",
			"nginx.ingress.kubernetes.io/unknown-fl": "x",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route == nil {
		t.Fatal("expected a route")
	}
	if got := countSeverity(res.Issues, SeverityWarning); got != 3 {
		t.Errorf("warnings = %d, want 3 (rate limit, auth, unregistered): %+v", got, res.Issues)
	}
}

func TestAnnotationPolicyDropSuppressesIssue(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/limit-rps": "100",
		}
	})

	opts := defaultOpts()
	opts.AnnotationPolicy = &AnnotationPolicy{
		Drop: []string{"nginx.ingress.kubernetes.io/limit-rps"},
	}

	res := NewEngine().ConvertIngress(ing, opts)
	if countSeverity(res.Issues, SeverityWarning) != 0 {
		t.Errorf("dropped annotation should raise no warning, got %+v", res.Issues)
	}
}

func TestNamedBackendPortFails(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port = networkingv1.ServiceBackendPort{Name: "http"}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route != nil {
		t.Error("expected no route when the only backend uses a named port")
	}
	if !res.Failed() {
		t.Errorf("expected an error severity issue, got %+v", res.Issues)
	}
}

func TestResourceBackendFails(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Spec.Rules[0].HTTP.Paths[0].Backend = networkingv1.IngressBackend{}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route != nil {
		t.Error("expected no route for a non-Service backend")
	}
	if !res.Failed() {
		t.Errorf("expected an error severity issue, got %+v", res.Issues)
	}
}

func TestMultipleRulesAndHostsAreDeduplicated(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		second := *i.Spec.Rules[0].DeepCopy()
		second.Host = "demo.localtest.me" // duplicate host
		second.HTTP.Paths[0].Path = "/api"

		third := *i.Spec.Rules[0].DeepCopy()
		third.Host = "other.localtest.me"

		i.Spec.Rules = append(i.Spec.Rules, second, third)
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route == nil {
		t.Fatalf("expected a route, issues: %+v", res.Issues)
	}

	if got := len(res.Route.Spec.Hostnames); got != 2 {
		t.Errorf("hostnames = %d, want 2 deduplicated", got)
	}
	if got := len(res.Route.Spec.Rules); got != 3 {
		t.Errorf("rules = %d, want 3 (one per path)", got)
	}
}

func TestTLSRaisesInfoIssue(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      []string{"demo.localtest.me"},
			SecretName: "demo-tls",
		}}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route == nil {
		t.Fatal("expected a route")
	}
	if res.Failed() {
		t.Errorf("TLS should not fail conversion: %+v", res.Issues)
	}
	if !hasSeverity(res.Issues, SeverityInfo) {
		t.Errorf("expected an info issue about TLS, got %+v", res.Issues)
	}
}

func TestDefaultBackendWarns(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Spec.DefaultBackend = &networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{
				Name: "fallback",
				Port: networkingv1.ServiceBackendPort{Number: 80},
			},
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if !hasSeverity(res.Issues, SeverityWarning) {
		t.Errorf("expected a warning for defaultBackend, got %+v", res.Issues)
	}
}

func TestNilIngressIsSafe(t *testing.T) {
	res := NewEngine().ConvertIngress(nil, defaultOpts())
	if res.Route != nil || len(res.Issues) != 0 {
		t.Errorf("nil ingress should yield an empty result, got %+v", res)
	}
}

func TestRegexPrefix(t *testing.T) {
	tests := map[string]string{
		"/api/v1/.*":   "/api/v1",
		"/api/(a|b)":   "/api",
		"^/exact$":     "/",
		"/users/[0-9]": "/users",
	}
	for in, want := range tests {
		if got := regexPrefix(in); got != want {
			t.Errorf("regexPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func hasSeverity(issues []Issue, sev string) bool {
	return countSeverity(issues, sev) > 0
}

func countSeverity(issues []Issue, sev string) int {
	n := 0
	for _, i := range issues {
		if i.Severity == sev {
			n++
		}
	}
	return n
}

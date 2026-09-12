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

package conversion

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestSSLRedirectTranslatesToSchemeRedirect(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/ssl-redirect": "true",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	filters := res.Route.Spec.Rules[0].Filters
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterRequestRedirect {
		t.Fatalf("filters = %+v, want one RequestRedirect", filters)
	}
	if got := *filters[0].RequestRedirect.Scheme; got != "https" {
		t.Errorf("scheme = %q, want https", got)
	}
}

func TestSSLRedirectFalseEmitsNoFilter(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/ssl-redirect": "false",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if got := len(res.Route.Spec.Rules[0].Filters); got != 0 {
		t.Errorf("filters = %d, want 0 when ssl-redirect is false", got)
	}
}

func TestPermanentRedirectTranslatesFullURL(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/permanent-redirect": "https://example.com/new-path",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	filters := res.Route.Spec.Rules[0].Filters
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterRequestRedirect {
		t.Fatalf("filters = %+v, want one RequestRedirect", filters)
	}
	redirect := filters[0].RequestRedirect
	if got := *redirect.StatusCode; got != 301 {
		t.Errorf("statusCode = %d, want 301", got)
	}
	if got := *redirect.Hostname; got != "example.com" {
		t.Errorf("hostname = %q, want example.com", got)
	}
	if got := *redirect.Path.ReplaceFullPath; got != "/new-path" {
		t.Errorf("path = %q, want /new-path", got)
	}
}

func TestPermanentRedirectInvalidURLWarns(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/permanent-redirect": "://not-a-url",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if got := len(res.Route.Spec.Rules[0].Filters); got != 0 {
		t.Errorf("filters = %d, want 0 for an invalid redirect URL", got)
	}
	if !hasSeverity(res.Issues, SeverityWarning) {
		t.Errorf("expected a warning, got %+v", res.Issues)
	}
}

func TestAppRootTranslatesToRedirectAndWarns(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/app-root": "/app",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	filters := res.Route.Spec.Rules[0].Filters
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterRequestRedirect {
		t.Fatalf("filters = %+v, want one RequestRedirect", filters)
	}
	if got := *filters[0].RequestRedirect.Path.ReplaceFullPath; got != "/app" {
		t.Errorf("path = %q, want /app", got)
	}
	if !hasSeverity(res.Issues, SeverityInfo) {
		t.Errorf("expected an info issue explaining the approximation, got %+v", res.Issues)
	}
}

func TestXForwardedPrefixTranslatesToHeaderModifier(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/x-forwarded-prefix": "/api",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	filters := res.Route.Spec.Rules[0].Filters
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterRequestHeaderModifier {
		t.Fatalf("filters = %+v, want one RequestHeaderModifier", filters)
	}
	set := filters[0].RequestHeaderModifier.Set
	if len(set) != 1 || set[0].Name != "X-Forwarded-Prefix" || set[0].Value != "/api" {
		t.Errorf("set = %+v, want X-Forwarded-Prefix: /api", set)
	}
}

func TestEveryRegisteredNginxAnnotationHasATranslator(t *testing.T) {
	for annotation, translator := range nginxTranslators {
		if translator == nil {
			t.Errorf("%s is registered with a nil translator", annotation)
		}
	}
}

func TestUnsupportedAnnotationsWarnWithSpecificGuidance(t *testing.T) {
	for _, annotation := range []string{
		"nginx.ingress.kubernetes.io/enable-cors",
		"nginx.ingress.kubernetes.io/canary",
		"nginx.ingress.kubernetes.io/whitelist-source-range",
		"nginx.ingress.kubernetes.io/configuration-snippet",
	} {
		ing := ingress(func(i *networkingv1.Ingress) {
			i.Annotations = map[string]string{annotation: "x"}
		})

		res := NewEngine().ConvertIngress(ing, defaultOpts())
		if !hasSeverity(res.Issues, SeverityWarning) {
			t.Errorf("%s: expected a warning, got %+v", annotation, res.Issues)
		}
		found := false
		for _, issue := range res.Issues {
			if issue.Recommendation != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected a non-empty recommendation", annotation)
		}
	}
}

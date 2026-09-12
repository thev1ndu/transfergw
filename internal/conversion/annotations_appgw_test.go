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

func TestAppGWSSLRedirectTranslatesToSchemeRedirect(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"appgw.ingress.kubernetes.io/ssl-redirect": "true",
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

func TestAppGWBackendPathPrefixTranslatesToURLRewrite(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"appgw.ingress.kubernetes.io/backend-path-prefix": "/api/",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	filters := res.Route.Spec.Rules[0].Filters
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterURLRewrite {
		t.Fatalf("filters = %+v, want one URLRewrite", filters)
	}
	if got := *filters[0].URLRewrite.Path.ReplacePrefixMatch; got != "/api/" {
		t.Errorf("replacePrefixMatch = %q, want /api/", got)
	}
}

func TestEveryRegisteredAppGWAnnotationHasATranslator(t *testing.T) {
	for annotation, translator := range appgwTranslators {
		if translator == nil {
			t.Errorf("%s is registered with a nil translator", annotation)
		}
	}
}

func TestUnsupportedAppGWAnnotationsWarnWithSpecificGuidance(t *testing.T) {
	for _, annotation := range []string{
		"appgw.ingress.kubernetes.io/waf-policy-for-path",
		"appgw.ingress.kubernetes.io/cookie-based-affinity",
		"appgw.ingress.kubernetes.io/rewrite-rule-set",
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

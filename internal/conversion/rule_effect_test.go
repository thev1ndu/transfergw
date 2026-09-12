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

func TestNginxAffinityCookieSetsSessionPersistence(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/affinity": "cookie",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	rule := res.Route.Spec.Rules[0]
	if rule.SessionPersistence == nil {
		t.Fatal("sessionPersistence is nil, want it set")
	}
	if got := *rule.SessionPersistence.Type; got != gatewayv1.CookieBasedSessionPersistence {
		t.Errorf("type = %q, want Cookie", got)
	}
}

func TestAppGWCookieBasedAffinitySetsSessionPersistence(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"appgw.ingress.kubernetes.io/cookie-based-affinity": "true",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	rule := res.Route.Spec.Rules[0]
	if rule.SessionPersistence == nil {
		t.Fatal("sessionPersistence is nil, want it set")
	}
}

func TestAppGWRequestTimeoutSetsRuleTimeouts(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"appgw.ingress.kubernetes.io/request-timeout": "45",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	rule := res.Route.Spec.Rules[0]
	if rule.Timeouts == nil || rule.Timeouts.Request == nil {
		t.Fatalf("timeouts = %+v, want a Request timeout", rule.Timeouts)
	}
	if got := string(*rule.Timeouts.Request); got != "45s" {
		t.Errorf("request timeout = %q, want 45s", got)
	}
}

func TestNginxProxyTimeoutsMergeToTheLongerBackendRequest(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/proxy-read-timeout": "30",
			"nginx.ingress.kubernetes.io/proxy-send-timeout": "90",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	rule := res.Route.Spec.Rules[0]
	if rule.Timeouts == nil || rule.Timeouts.BackendRequest == nil {
		t.Fatalf("timeouts = %+v, want a BackendRequest timeout", rule.Timeouts)
	}
	if got := string(*rule.Timeouts.BackendRequest); got != "90s" {
		t.Errorf("backendRequest = %q, want the longer value 90s, not truncated to the shorter one", got)
	}
}

func TestNginxCORSCombinesSiblingAnnotationsThroughTheEngine(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/enable-cors":       "true",
			"nginx.ingress.kubernetes.io/cors-allow-origin": "https://example.com",
			"nginx.ingress.kubernetes.io/cors-max-age":      "120",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	filters := res.Route.Spec.Rules[0].Filters
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterCORS {
		t.Fatalf("filters = %+v, want one CORS filter", filters)
	}
	cors := filters[0].CORS
	if len(cors.AllowOrigins) != 1 || cors.AllowOrigins[0] != "https://example.com" {
		t.Errorf("allowOrigins = %+v, want [https://example.com]", cors.AllowOrigins)
	}
	if cors.MaxAge != 120 {
		t.Errorf("maxAge = %d, want 120", cors.MaxAge)
	}
}

func TestNginxSessionCookieNameThroughTheEngine(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/affinity":            "cookie",
			"nginx.ingress.kubernetes.io/session-cookie-name": "my_session",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	rule := res.Route.Spec.Rules[0]
	if rule.SessionPersistence == nil {
		t.Fatal("sessionPersistence is nil, want it set")
	}
	if rule.SessionPersistence.SessionName == nil || *rule.SessionPersistence.SessionName != "my_session" {
		t.Errorf("sessionName = %v, want my_session", rule.SessionPersistence.SessionName)
	}
}

func TestInvalidTimeoutValueWarnsWithoutSettingTimeouts(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/proxy-read-timeout": "not-a-number",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	rule := res.Route.Spec.Rules[0]
	if rule.Timeouts != nil {
		t.Errorf("timeouts = %+v, want nil for an invalid value", rule.Timeouts)
	}
	if !hasSeverity(res.Issues, SeverityWarning) {
		t.Errorf("expected a warning, got %+v", res.Issues)
	}
}

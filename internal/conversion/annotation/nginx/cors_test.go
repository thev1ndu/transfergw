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

package nginx

import (
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestCORSDisabledEmitsNoFilter(t *testing.T) {
	tr := &CORSTranslator{}
	filters, issue := tr.TranslateWithContext(Prefix+"enable-cors", "false", map[string]string{
		Prefix + "enable-cors": "false",
	})
	if issue != nil || len(filters) != 0 {
		t.Errorf("filters = %+v, issue = %+v, want none when enable-cors is false", filters, issue)
	}
}

func TestCORSDefaultsToAllowAllOriginsAndCredentials(t *testing.T) {
	tr := &CORSTranslator{}
	all := map[string]string{Prefix + "enable-cors": "true"}
	filters, issue := tr.TranslateWithContext(Prefix+"enable-cors", "true", all)
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterCORS {
		t.Fatalf("filters = %+v, want one CORS filter", filters)
	}
	cors := filters[0].CORS
	if len(cors.AllowOrigins) != 1 || cors.AllowOrigins[0] != "*" {
		t.Errorf("allowOrigins = %+v, want [*] by default", cors.AllowOrigins)
	}
	if cors.AllowCredentials == nil || !*cors.AllowCredentials {
		t.Errorf("allowCredentials = %v, want true by default", cors.AllowCredentials)
	}
}

func TestCORSCombinesEverySiblingAnnotation(t *testing.T) {
	tr := &CORSTranslator{}
	all := map[string]string{
		Prefix + "enable-cors":            "true",
		Prefix + "cors-allow-origin":      "https://example.com,https://foo.example.com",
		Prefix + "cors-allow-methods":     "GET,POST",
		Prefix + "cors-allow-headers":     "X-Custom-Header",
		Prefix + "cors-allow-credentials": "false",
		Prefix + "cors-expose-headers":    "X-Exposed",
		Prefix + "cors-max-age":           "600",
	}
	filters, issue := tr.TranslateWithContext(Prefix+"enable-cors", "true", all)
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	cors := filters[0].CORS

	if len(cors.AllowOrigins) != 2 || cors.AllowOrigins[0] != "https://example.com" {
		t.Errorf("allowOrigins = %+v, want the two configured origins", cors.AllowOrigins)
	}
	if len(cors.AllowMethods) != 2 || cors.AllowMethods[0] != "GET" {
		t.Errorf("allowMethods = %+v, want [GET POST]", cors.AllowMethods)
	}
	if len(cors.AllowHeaders) != 1 || cors.AllowHeaders[0] != "X-Custom-Header" {
		t.Errorf("allowHeaders = %+v, want [X-Custom-Header]", cors.AllowHeaders)
	}
	if cors.AllowCredentials == nil || *cors.AllowCredentials {
		t.Errorf("allowCredentials = %v, want false (explicitly configured)", cors.AllowCredentials)
	}
	if len(cors.ExposeHeaders) != 1 || cors.ExposeHeaders[0] != "X-Exposed" {
		t.Errorf("exposeHeaders = %+v, want [X-Exposed]", cors.ExposeHeaders)
	}
	if cors.MaxAge != 600 {
		t.Errorf("maxAge = %d, want 600", cors.MaxAge)
	}
}

func TestCORSCompanionAnnotationsAreRegisteredNoOps(t *testing.T) {
	for _, key := range []string{
		Prefix + "cors-allow-origin",
		Prefix + "cors-allow-methods",
		Prefix + "cors-allow-headers",
		Prefix + "cors-allow-credentials",
		Prefix + "cors-expose-headers",
		Prefix + "cors-max-age",
	} {
		tr, ok := Translators[key]
		if !ok {
			t.Fatalf("%s: no translator registered", key)
		}
		filters, issue := tr.Translate(key, "x")
		if len(filters) != 0 || issue != nil {
			t.Errorf("%s: filters = %+v, issue = %+v, want a no-op on its own", key, filters, issue)
		}
	}
}

func TestSessionCookieNameOnlyAppliesWhenAffinityIsCookie(t *testing.T) {
	tr := &SessionCookieNameTranslator{}

	_, issue := tr.Translate(Prefix+"session-cookie-name", "my_session")
	if issue != nil {
		t.Fatalf("Translate issue = %+v, want none (only meaningful via context)", issue)
	}

	effect, issue := tr.EffectWithContext(Prefix+"session-cookie-name", "my_session", map[string]string{
		Prefix + "affinity": "cookie",
	})
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if effect == nil || effect.SessionPersistence == nil {
		t.Fatalf("effect = %+v, want a SessionPersistence", effect)
	}
	if got := *effect.SessionPersistence.SessionName; got != "my_session" {
		t.Errorf("sessionName = %q, want my_session", got)
	}
	if got := *effect.SessionPersistence.Type; got != gatewayv1.CookieBasedSessionPersistence {
		t.Errorf("type = %q, want Cookie", got)
	}
}

func TestSessionCookieNameNoOpWithoutCookieAffinity(t *testing.T) {
	tr := &SessionCookieNameTranslator{}
	effect, issue := tr.EffectWithContext(Prefix+"session-cookie-name", "my_session", map[string]string{})
	if effect != nil || issue != nil {
		t.Errorf("effect = %+v, issue = %+v, want none without affinity: cookie", effect, issue)
	}
}

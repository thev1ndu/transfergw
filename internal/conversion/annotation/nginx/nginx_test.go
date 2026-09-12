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

	"github.com/thev1ndu/transfergw/internal/conversion/translator"
)

func TestSSLRedirectTranslatesToSchemeRedirect(t *testing.T) {
	tr := Translators[Prefix+"ssl-redirect"]
	filters, issue := tr.Translate(Prefix+"ssl-redirect", "true")
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterRequestRedirect {
		t.Fatalf("filters = %+v, want one RequestRedirect", filters)
	}
	if got := *filters[0].RequestRedirect.Scheme; got != "https" {
		t.Errorf("scheme = %q, want https", got)
	}
}

func TestSSLRedirectFalseEmitsNoFilter(t *testing.T) {
	tr := Translators[Prefix+"ssl-redirect"]
	filters, issue := tr.Translate(Prefix+"ssl-redirect", "false")
	if issue != nil || len(filters) != 0 {
		t.Errorf("filters = %+v, issue = %+v, want none when ssl-redirect is false", filters, issue)
	}
}

func TestRewriteAnnotationBecomesFilter(t *testing.T) {
	tr := &RewriteTranslator{}
	filters, issue := tr.Translate(Prefix+"rewrite-target", "/")
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterURLRewrite {
		t.Fatalf("filters = %+v, want one URLRewrite", filters)
	}
	if got := *filters[0].URLRewrite.Path.ReplacePrefixMatch; got != "/" {
		t.Errorf("replacePrefixMatch = %q, want /", got)
	}
}

func TestRewriteWithCaptureGroupWarnsAndEmitsNoFilter(t *testing.T) {
	tr := &RewriteTranslator{}
	filters, issue := tr.Translate(Prefix+"rewrite-target", "/$1")
	if len(filters) != 0 {
		t.Errorf("filters = %d, want 0 for an inexpressible rewrite", len(filters))
	}
	if issue == nil || issue.Severity != translator.SeverityWarning {
		t.Errorf("expected a warning issue, got %+v", issue)
	}
}

func TestPermanentRedirectTranslatesFullURL(t *testing.T) {
	tr := &RedirectTranslator{StatusCode: 301}
	filters, issue := tr.Translate(Prefix+"permanent-redirect", "https://example.com/new-path")
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
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
	tr := &RedirectTranslator{StatusCode: 301}
	filters, issue := tr.Translate(Prefix+"permanent-redirect", "://not-a-url")
	if len(filters) != 0 {
		t.Errorf("filters = %d, want 0 for an invalid redirect URL", len(filters))
	}
	if issue == nil || issue.Severity != translator.SeverityWarning {
		t.Errorf("expected a warning issue, got %+v", issue)
	}
}

func TestAppRootTranslatesToRedirectAndWarns(t *testing.T) {
	tr := &AppRootTranslator{}
	filters, issue := tr.Translate(Prefix+"app-root", "/app")
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterRequestRedirect {
		t.Fatalf("filters = %+v, want one RequestRedirect", filters)
	}
	if got := *filters[0].RequestRedirect.Path.ReplaceFullPath; got != "/app" {
		t.Errorf("path = %q, want /app", got)
	}
	if issue == nil || issue.Severity != translator.SeverityInfo {
		t.Errorf("expected an info issue explaining the approximation, got %+v", issue)
	}
}

func TestXForwardedPrefixTranslatesToHeaderModifier(t *testing.T) {
	tr := &XForwardedPrefixTranslator{}
	filters, issue := tr.Translate(Prefix+"x-forwarded-prefix", "/api")
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterRequestHeaderModifier {
		t.Fatalf("filters = %+v, want one RequestHeaderModifier", filters)
	}
	set := filters[0].RequestHeaderModifier.Set
	if len(set) != 1 || set[0].Name != "X-Forwarded-Prefix" || set[0].Value != "/api" {
		t.Errorf("set = %+v, want X-Forwarded-Prefix: /api", set)
	}
}

func TestEveryRegisteredAnnotationHasATranslator(t *testing.T) {
	for key, translator := range Translators {
		if translator == nil {
			t.Errorf("%s is registered with a nil translator", key)
		}
	}
}

func TestUnsupportedAnnotationsWarnWithSpecificGuidance(t *testing.T) {
	for _, key := range []string{
		Prefix + "session-cookie-hash",
		Prefix + "canary",
		Prefix + "whitelist-source-range",
		Prefix + "configuration-snippet",
	} {
		tr, ok := Translators[key]
		if !ok {
			t.Fatalf("%s: no translator registered", key)
		}
		_, issue := tr.Translate(key, "x")
		if issue == nil || issue.Severity != translator.SeverityWarning {
			t.Errorf("%s: expected a warning, got %+v", key, issue)
		}
		if issue != nil && issue.Recommendation == "" {
			t.Errorf("%s: expected a non-empty recommendation", key)
		}
	}
}

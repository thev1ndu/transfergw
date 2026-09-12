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

package appgw

import (
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
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

func TestBackendPathPrefixTranslatesToURLRewrite(t *testing.T) {
	tr := &BackendPathPrefixTranslator{}
	filters, issue := tr.Translate(Prefix+"backend-path-prefix", "/api/")
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if len(filters) != 1 || filters[0].Type != gatewayv1.HTTPRouteFilterURLRewrite {
		t.Fatalf("filters = %+v, want one URLRewrite", filters)
	}
	if got := *filters[0].URLRewrite.Path.ReplacePrefixMatch; got != "/api/" {
		t.Errorf("replacePrefixMatch = %q, want /api/", got)
	}
}

func TestEveryRegisteredAnnotationHasATranslator(t *testing.T) {
	for annotation, translator := range Translators {
		if translator == nil {
			t.Errorf("%s is registered with a nil translator", annotation)
		}
	}
}

func TestUnsupportedAnnotationsWarnWithSpecificGuidance(t *testing.T) {
	for _, key := range []string{
		Prefix + "waf-policy-for-path",
		Prefix + "cookie-based-affinity-distinct-name",
		Prefix + "rewrite-rule-set",
	} {
		tr, ok := Translators[key]
		if !ok {
			t.Fatalf("%s: no translator registered", key)
		}
		_, issue := tr.Translate(key, "x")
		if issue == nil {
			t.Fatalf("%s: expected an issue, got none", key)
		}
		if issue.Recommendation == "" {
			t.Errorf("%s: expected a non-empty recommendation", key)
		}
	}
}

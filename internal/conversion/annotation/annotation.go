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

// Package annotation defines the shared contract every vendor annotation
// package (internal/conversion/annotations/...) implements, plus a couple of
// generic building blocks reused across vendors.
//
// It exists as its own leaf package - rather than living in
// internal/conversion, which is what the engine.Translator/Issue names would
// suggest - because the engine imports each vendor package to register its
// translators, and a vendor package needs Translator/Issue/Severity* to
// implement one. Defining those in internal/conversion would make that a
// cycle: internal/conversion -> annotations/nginx -> internal/conversion.
package annotation

import (
	"fmt"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Severity levels reported on an Issue.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Issue is a warning or error raised while converting an Ingress.
type Issue struct {
	Ingress        string
	Message        string
	Severity       string
	Recommendation string
}

// Translator converts a single Ingress annotation into HTTPRoute filters.
// Returning a nil filter slice with a non-nil issue means the annotation has
// no portable Gateway API equivalent.
type Translator interface {
	Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue)
}

// SSLRedirectTranslator maps an nginx- or AGIC-style "ssl-redirect" boolean
// annotation onto a scheme redirect. A "false" value needs no filter: not
// redirecting is already the default HTTPRoute behaviour.
type SSLRedirectTranslator struct{}

func (t *SSLRedirectTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	if value != "true" {
		return nil, nil
	}
	return []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
			Scheme: ptr.To("https"),
		},
	}}, nil
}

// unsupported reports that an annotation has no portable Gateway API
// equivalent, with vendor-specific guidance on how to reproduce it.
type unsupported struct {
	recommendation string
}

func (t *unsupported) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: t.recommendation,
	}
}

// Unsupported builds a Translator for an annotation that has no safe
// automatic translation, so it still surfaces a specific, actionable warning
// instead of the generic "no registered translator" one.
func Unsupported(recommendation string) Translator {
	return &unsupported{recommendation: recommendation}
}

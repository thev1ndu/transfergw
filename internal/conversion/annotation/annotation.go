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
	"strconv"
	"strings"

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

// RuleEffect captures per-Ingress modifications a Translator wants applied to
// the enclosing HTTPRouteRule itself, rather than a filter within it.
// SessionPersistence and Timeouts are HTTPRouteRule fields, not filters -
// nothing a Translator returns from Translate can reach them.
type RuleEffect struct {
	SessionPersistence *gatewayv1.SessionPersistence
	Timeouts           *gatewayv1.HTTPRouteTimeouts
}

// RuleEffector is implemented by a Translator that also needs to set fields
// on the enclosing HTTPRouteRule (session affinity, timeouts) that a filter
// cannot express. A Translator implementing this still implements Translate
// too (returning nil, nil when it has no filter to contribute), so it works
// as a normal, filter-only Translator wherever RuleEffector isn't checked.
type RuleEffector interface {
	Effect(key, value string) (*RuleEffect, *Issue)
}

// ParseSecondsDuration parses a plain integer number of seconds (as nginx and
// AGIC both encode their timeout annotations) into a Gateway API Duration.
func ParseSecondsDuration(key, value string) (*gatewayv1.Duration, *Issue) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return nil, &Issue{
			Severity:       SeverityWarning,
			Message:        fmt.Sprintf("%s value %q is not a non-negative whole number of seconds", key, value),
			Recommendation: "Set this annotation to a whole number of seconds, e.g. \"30\".",
		}
	}
	d := gatewayv1.Duration(fmt.Sprintf("%ds", n))
	return &d, nil
}

// CookieSessionPersistenceTranslator maps an nginx- or AGIC-style boolean
// "enable cookie affinity" annotation onto HTTPRouteRule.sessionPersistence.
// It contributes no filter - Translate always returns nil, nil - only a
// RuleEffect, so it's used via RuleEffector.
type CookieSessionPersistenceTranslator struct{}

func (t *CookieSessionPersistenceTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, nil
}

func (t *CookieSessionPersistenceTranslator) Effect(key, value string) (*RuleEffect, *Issue) {
	if value != "true" && value != "cookie" {
		return nil, nil
	}
	cookieType := gatewayv1.CookieBasedSessionPersistence
	return &RuleEffect{
		SessionPersistence: &gatewayv1.SessionPersistence{Type: &cookieType},
	}, nil
}

// BackendRequestTimeoutTranslator maps a per-request-to-backend timeout
// annotation (nginx's proxy-read-timeout/proxy-send-timeout) onto
// HTTPRouteRule.timeouts.backendRequest. It contributes no filter - only a
// RuleEffect, so it's used via RuleEffector.
type BackendRequestTimeoutTranslator struct{}

func (t *BackendRequestTimeoutTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, nil
}

func (t *BackendRequestTimeoutTranslator) Effect(key, value string) (*RuleEffect, *Issue) {
	d, issue := ParseSecondsDuration(key, value)
	if d == nil {
		return nil, issue
	}
	return &RuleEffect{Timeouts: &gatewayv1.HTTPRouteTimeouts{BackendRequest: d}}, nil
}

// RequestTimeoutTranslator maps an overall request-timeout annotation
// (AGIC's request-timeout) onto HTTPRouteRule.timeouts.request. It
// contributes no filter - only a RuleEffect, so it's used via RuleEffector.
type RequestTimeoutTranslator struct{}

func (t *RequestTimeoutTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, nil
}

func (t *RequestTimeoutTranslator) Effect(key, value string) (*RuleEffect, *Issue) {
	d, issue := ParseSecondsDuration(key, value)
	if d == nil {
		return nil, issue
	}
	return &RuleEffect{Timeouts: &gatewayv1.HTTPRouteTimeouts{Request: d}}, nil
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

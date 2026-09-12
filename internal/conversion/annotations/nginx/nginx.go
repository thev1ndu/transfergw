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

// Package nginx translates ingress-nginx annotations into Gateway API
// filters, or an explicit warning where there's no portable equivalent.
package nginx

import (
	"fmt"
	"net/url"
	"strings"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/thev1ndu/transfergw/internal/conversion/annotation"
)

// Prefix is shared by every ingress-nginx annotation.
const Prefix = "nginx.ingress.kubernetes.io/"

// Translators maps every ingress-nginx annotation this engine knows about to
// the Translator that handles it. Annotations with a portable Gateway API
// filter get a real translation; everything else still gets an entry here,
// using annotation.Unsupported, so the operator reports a specific,
// actionable warning instead of a generic "no translator" message.
//
// To add support for another nginx annotation, implement annotation.Translator
// (or reuse annotation.Unsupported) and add one entry here.
var Translators = map[string]annotation.Translator{
	// --- Have a portable Gateway API filter -----------------------------
	Prefix + "rewrite-target":     &RewriteTranslator{},
	Prefix + "ssl-redirect":       &annotation.SSLRedirectTranslator{},
	Prefix + "force-ssl-redirect": &annotation.SSLRedirectTranslator{},
	Prefix + "permanent-redirect": &RedirectTranslator{StatusCode: 301},
	Prefix + "temporal-redirect":  &RedirectTranslator{StatusCode: 302},
	Prefix + "app-root":           &AppRootTranslator{},
	Prefix + "x-forwarded-prefix": &XForwardedPrefixTranslator{},

	// --- No portable equivalent: reported, not silently dropped ---------
	Prefix + "auth-type": &AuthTranslator{},
	Prefix + "auth-url":  &AuthTranslator{},
	Prefix + "auth-secret": annotation.Unsupported(
		"Express authentication with your implementation's policy CRD."),
	Prefix + "auth-realm": annotation.Unsupported(
		"Express authentication with your implementation's policy CRD."),
	Prefix + "auth-signin": annotation.Unsupported(
		"Express authentication with your implementation's policy CRD."),
	Prefix + "auth-response-headers": annotation.Unsupported(
		"Express authentication with your implementation's policy CRD."),
	Prefix + "auth-snippet": annotation.Unsupported(
		"Gateway API has no request-snippet escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),

	Prefix + "limit-rps":              &RateLimitTranslator{},
	Prefix + "limit-rpm":              &RateLimitTranslator{},
	Prefix + "limit-burst-multiplier": &RateLimitTranslator{},
	Prefix + "limit-connections":      &RateLimitTranslator{},

	Prefix + "whitelist-source-range": annotation.Unsupported(
		"Use your implementation's IP-allowlist policy CRD (e.g. a BackendTrafficPolicy " +
			"or SecurityPolicy) instead."),

	// enable-cors is the switch; the cors-* siblings below are only ever
	// read as context by its TranslateWithContext, via annotation.NoOp so
	// they're still "known" annotations rather than falling through as
	// unregistered ones.
	Prefix + "enable-cors":            &CORSTranslator{},
	Prefix + "cors-allow-origin":      annotation.NoOp(),
	Prefix + "cors-allow-methods":     annotation.NoOp(),
	Prefix + "cors-allow-headers":     annotation.NoOp(),
	Prefix + "cors-allow-credentials": annotation.NoOp(),
	Prefix + "cors-expose-headers":    annotation.NoOp(),
	Prefix + "cors-max-age":           annotation.NoOp(),

	Prefix + "proxy-body-size": annotation.Unsupported(
		"Set request body size limits with your implementation's traffic policy CRD."),
	Prefix + "proxy-connect-timeout": annotation.Unsupported(
		"Set connection timeouts with your implementation's traffic policy CRD."),
	Prefix + "proxy-read-timeout": &annotation.BackendRequestTimeoutTranslator{},
	Prefix + "proxy-send-timeout": &annotation.BackendRequestTimeoutTranslator{},
	Prefix + "backend-protocol": annotation.Unsupported(
		"Set the backend Service port's appProtocol field instead."),

	// canary/canary-weight are handled at the controller level, not here:
	// the controller looks for a primary Ingress sharing this canary's
	// namespace/host/single path and, if found, merges this into a second
	// weighted HTTPBackendRef on the primary's route instead of converting
	// it as its own HTTPRoute. This entry (and its message) only fires when
	// that pairing fails - no matching primary, more than one candidate, a
	// multi-host/multi-path Ingress, or a missing/invalid canary-weight.
	Prefix + "canary": annotation.Unsupported(
		"No matching primary Ingress was found to pair this canary with (same namespace, " +
			"host, and a single path, plus a valid canary-weight) - model the weighted " +
			"backendRefs explicitly instead."),
	Prefix + "canary-weight": annotation.Unsupported(
		"Set the weight on the corresponding backendRef instead."),
	Prefix + "canary-by-header": annotation.Unsupported(
		"Use HTTPRoute header matches across two rules instead of a canary annotation."),
	Prefix + "canary-by-cookie": annotation.Unsupported(
		"Use HTTPRoute cookie/header matches across two rules instead of a canary annotation."),

	Prefix + "affinity":            &annotation.CookieSessionPersistenceTranslator{},
	Prefix + "session-cookie-name": &SessionCookieNameTranslator{},

	Prefix + "session-cookie-hash": annotation.Unsupported(
		"Gateway API's sessionPersistence has no cookie-hashing knob; the cookie value " +
			"is opaque to the client either way, so this annotation has no effect to port."),

	Prefix + "configuration-snippet": annotation.Unsupported(
		"Gateway API has no raw-config escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),
	Prefix + "server-snippet": annotation.Unsupported(
		"Gateway API has no raw-config escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),
	Prefix + "stream-snippet": annotation.Unsupported(
		"Gateway API has no raw-config escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),

	Prefix + "custom-http-errors": annotation.Unsupported(
		"Use your implementation's custom-error-response policy CRD instead."),
	Prefix + "default-backend": annotation.Unsupported(
		"Add an explicit catch-all HTTPRoute rule with path prefix \"/\" instead."),
	Prefix + "service-upstream": annotation.Unsupported(
		"Gateway API always routes through the Service; this annotation has no effect to port."),
	Prefix + "upstream-vhost": annotation.Unsupported(
		"Gateway API filters cannot rewrite the Host header; use your implementation's " +
			"traffic policy CRD instead."),
	Prefix + "ssl-passthrough": annotation.Unsupported(
		"Configure TLS passthrough on the Gateway listener (protocol: TLS) instead."),
}

// RewriteTranslator maps nginx's rewrite-target onto a URLRewrite filter.
type RewriteTranslator struct{}

func (t *RewriteTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	if value == "" {
		return nil, nil
	}
	if strings.ContainsAny(value, "$()") {
		return nil, &annotation.Issue{
			Severity: annotation.SeverityWarning,
			Message: fmt.Sprintf("rewrite-target %q uses capture groups, which URLRewrite "+
				"cannot express", value),
			Recommendation: "Replace the capture-group rewrite with a static prefix rewrite.",
		}
	}
	return []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterURLRewrite,
		URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
			Path: &gatewayv1.HTTPPathModifier{
				Type:               gatewayv1.PrefixMatchHTTPPathModifier,
				ReplacePrefixMatch: ptr.To(value),
			},
		},
	}}, nil
}

// RedirectTranslator maps nginx's permanent-redirect and temporal-redirect
// onto a RequestRedirect filter at the given status code.
type RedirectTranslator struct {
	StatusCode int
}

func (t *RedirectTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	if value == "" {
		return nil, nil
	}
	target, err := url.Parse(value)
	if err != nil {
		return nil, &annotation.Issue{
			Severity:       annotation.SeverityWarning,
			Message:        fmt.Sprintf("%s value %q is not a valid URL", key, value),
			Recommendation: "Set this annotation to a full URL, e.g. https://example.com/new-path.",
		}
	}

	redirect := &gatewayv1.HTTPRequestRedirectFilter{
		StatusCode: ptr.To(t.StatusCode),
	}
	if target.Scheme != "" {
		redirect.Scheme = ptr.To(target.Scheme)
	}
	if target.Host != "" {
		redirect.Hostname = ptr.To(gatewayv1.PreciseHostname(target.Hostname()))
	}
	if target.Path != "" {
		redirect.Path = &gatewayv1.HTTPPathModifier{
			Type:            gatewayv1.FullPathHTTPPathModifier,
			ReplaceFullPath: ptr.To(target.Path),
		}
	}

	return []gatewayv1.HTTPRouteFilter{{
		Type:            gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: redirect,
	}}, nil
}

// AppRootTranslator maps nginx's app-root onto a full-path redirect.
//
// nginx only applies app-root to the bare "/" request; this filter applies
// to every path on the rule, which is a close but imperfect approximation.
type AppRootTranslator struct{}

func (t *AppRootTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	if value == "" {
		return nil, nil
	}
	filters := []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
			StatusCode: ptr.To(302),
			Path: &gatewayv1.HTTPPathModifier{
				Type:            gatewayv1.FullPathHTTPPathModifier,
				ReplaceFullPath: ptr.To(value),
			},
		},
	}}
	return filters, &annotation.Issue{
		Severity: annotation.SeverityInfo,
		Message: fmt.Sprintf("app-root=%s was approximated as a redirect on every path "+
			"in this rule, not just \"/\"", value),
		Recommendation: "Split the root path into its own HTTPRoute rule if this is too broad.",
	}
}

// XForwardedPrefixTranslator maps nginx's x-forwarded-prefix onto a request
// header addition.
type XForwardedPrefixTranslator struct{}

func (t *XForwardedPrefixTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	if value == "" {
		return nil, nil
	}
	return []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
		RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
			Set: []gatewayv1.HTTPHeader{{
				Name:  "X-Forwarded-Prefix",
				Value: value,
			}},
		},
	}}, nil
}

// RateLimitTranslator reports that rate limiting needs an out-of-band policy.
type RateLimitTranslator struct{}

func (t *RateLimitTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	return nil, &annotation.Issue{
		Severity:       annotation.SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: "Express rate limiting with your implementation's policy CRD.",
	}
}

// AuthTranslator reports that external auth needs an out-of-band policy.
type AuthTranslator struct{}

func (t *AuthTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	return nil, &annotation.Issue{
		Severity:       annotation.SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: "Express authentication with your implementation's policy CRD.",
	}
}

// SessionCookieNameTranslator maps session-cookie-name onto
// HTTPRouteRule.sessionPersistence.sessionName - but only once the sibling
// affinity annotation has actually enabled cookie-based affinity. A custom
// cookie name on its own, without affinity: cookie, has no effect in nginx
// either. It needs annotation.ContextualRuleEffector to see that sibling.
type SessionCookieNameTranslator struct{}

func (t *SessionCookieNameTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	return nil, nil
}

func (t *SessionCookieNameTranslator) EffectWithContext(
	key, value string,
	all map[string]string,
) (*annotation.RuleEffect, *annotation.Issue) {
	if value == "" || all[Prefix+"affinity"] != "cookie" {
		return nil, nil
	}
	cookieType := gatewayv1.CookieBasedSessionPersistence
	return &annotation.RuleEffect{
		SessionPersistence: &gatewayv1.SessionPersistence{
			Type:        &cookieType,
			SessionName: ptr.To(value),
		},
	}, nil
}

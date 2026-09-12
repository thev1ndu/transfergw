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
	"fmt"
	"net/url"
	"strings"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// nginxAnnotationPrefix is shared by every ingress-nginx annotation.
const nginxAnnotationPrefix = "nginx.ingress.kubernetes.io/"

// nginxTranslators maps every ingress-nginx annotation this engine knows
// about to the Translator that handles it. Annotations with a portable
// Gateway API filter get a real translation; everything else still gets an
// entry here, using unsupportedNginxAnnotation, so the operator reports a
// specific, actionable warning instead of a generic "no translator" message.
//
// To add support for another nginx annotation, implement Translator (or
// reuse unsupportedNginxAnnotation) and add one entry here.
var nginxTranslators = map[string]Translator{
	// --- Have a portable Gateway API filter -----------------------------
	nginxAnnotationPrefix + "rewrite-target":     &RewriteTranslator{},
	nginxAnnotationPrefix + "ssl-redirect":       &SSLRedirectTranslator{},
	nginxAnnotationPrefix + "force-ssl-redirect": &SSLRedirectTranslator{},
	nginxAnnotationPrefix + "permanent-redirect": &RedirectTranslator{StatusCode: 301},
	nginxAnnotationPrefix + "temporal-redirect":  &RedirectTranslator{StatusCode: 302},
	nginxAnnotationPrefix + "app-root":           &AppRootTranslator{},
	nginxAnnotationPrefix + "x-forwarded-prefix": &XForwardedPrefixTranslator{},

	// --- No portable equivalent: reported, not silently dropped ---------
	nginxAnnotationPrefix + "auth-type": &AuthTranslator{},
	nginxAnnotationPrefix + "auth-url":  &AuthTranslator{},
	nginxAnnotationPrefix + "auth-secret": unsupportedNginxAnnotation(
		"Express authentication with your implementation's policy CRD."),
	nginxAnnotationPrefix + "auth-realm": unsupportedNginxAnnotation(
		"Express authentication with your implementation's policy CRD."),
	nginxAnnotationPrefix + "auth-signin": unsupportedNginxAnnotation(
		"Express authentication with your implementation's policy CRD."),
	nginxAnnotationPrefix + "auth-response-headers": unsupportedNginxAnnotation(
		"Express authentication with your implementation's policy CRD."),
	nginxAnnotationPrefix + "auth-snippet": unsupportedNginxAnnotation(
		"Gateway API has no request-snippet escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),

	nginxAnnotationPrefix + "limit-rps":              &RateLimitTranslator{},
	nginxAnnotationPrefix + "limit-rpm":              &RateLimitTranslator{},
	nginxAnnotationPrefix + "limit-burst-multiplier": &RateLimitTranslator{},
	nginxAnnotationPrefix + "limit-connections":      &RateLimitTranslator{},

	nginxAnnotationPrefix + "whitelist-source-range": unsupportedNginxAnnotation(
		"Use your implementation's IP-allowlist policy CRD (e.g. a BackendTrafficPolicy " +
			"or SecurityPolicy) instead."),

	nginxAnnotationPrefix + "enable-cors": unsupportedNginxAnnotation(
		"Gateway API has a core CORS filter (HTTPRouteFilterCORS); configure it " +
			"directly on the generated HTTPRoute instead of via annotation."),
	nginxAnnotationPrefix + "cors-allow-origin": unsupportedNginxAnnotation(
		"Configure the HTTPRoute's CORS filter allowOrigins field instead."),
	nginxAnnotationPrefix + "cors-allow-methods": unsupportedNginxAnnotation(
		"Configure the HTTPRoute's CORS filter allowMethods field instead."),
	nginxAnnotationPrefix + "cors-allow-headers": unsupportedNginxAnnotation(
		"Configure the HTTPRoute's CORS filter allowHeaders field instead."),
	nginxAnnotationPrefix + "cors-allow-credentials": unsupportedNginxAnnotation(
		"Configure the HTTPRoute's CORS filter allowCredentials field instead."),
	nginxAnnotationPrefix + "cors-expose-headers": unsupportedNginxAnnotation(
		"Configure the HTTPRoute's CORS filter exposeHeaders field instead."),
	nginxAnnotationPrefix + "cors-max-age": unsupportedNginxAnnotation(
		"Configure the HTTPRoute's CORS filter maxAge field instead."),

	nginxAnnotationPrefix + "proxy-body-size": unsupportedNginxAnnotation(
		"Set request body size limits with your implementation's traffic policy CRD."),
	nginxAnnotationPrefix + "proxy-connect-timeout": unsupportedNginxAnnotation(
		"Set connection timeouts with your implementation's traffic policy CRD."),
	nginxAnnotationPrefix + "proxy-read-timeout": unsupportedNginxAnnotation(
		"Set backend timeouts with HTTPRouteRule.timeouts.backendRequest instead."),
	nginxAnnotationPrefix + "proxy-send-timeout": unsupportedNginxAnnotation(
		"Set backend timeouts with HTTPRouteRule.timeouts.backendRequest instead."),
	nginxAnnotationPrefix + "backend-protocol": unsupportedNginxAnnotation(
		"Set the backend Service port's appProtocol field instead."),

	nginxAnnotationPrefix + "canary": unsupportedNginxAnnotation(
		"Gateway API expresses canaries as weighted backendRefs on one HTTPRoute, " +
			"not a second annotated Ingress; model both backends explicitly."),
	nginxAnnotationPrefix + "canary-weight": unsupportedNginxAnnotation(
		"Set the weight on the corresponding backendRef instead."),
	nginxAnnotationPrefix + "canary-by-header": unsupportedNginxAnnotation(
		"Use HTTPRoute header matches across two rules instead of a canary annotation."),
	nginxAnnotationPrefix + "canary-by-cookie": unsupportedNginxAnnotation(
		"Use HTTPRoute cookie/header matches across two rules instead of a canary annotation."),

	nginxAnnotationPrefix + "affinity": unsupportedNginxAnnotation(
		"Use your implementation's session-affinity/load-balancing policy CRD instead."),
	nginxAnnotationPrefix + "session-cookie-name": unsupportedNginxAnnotation(
		"Use your implementation's session-affinity policy CRD instead."),
	nginxAnnotationPrefix + "session-cookie-hash": unsupportedNginxAnnotation(
		"Use your implementation's session-affinity policy CRD instead."),

	nginxAnnotationPrefix + "configuration-snippet": unsupportedNginxAnnotation(
		"Gateway API has no raw-config escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),
	nginxAnnotationPrefix + "server-snippet": unsupportedNginxAnnotation(
		"Gateway API has no raw-config escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),
	nginxAnnotationPrefix + "stream-snippet": unsupportedNginxAnnotation(
		"Gateway API has no raw-config escape hatch; reproduce this with an " +
			"implementation-specific filter or policy."),

	nginxAnnotationPrefix + "custom-http-errors": unsupportedNginxAnnotation(
		"Use your implementation's custom-error-response policy CRD instead."),
	nginxAnnotationPrefix + "default-backend": unsupportedNginxAnnotation(
		"Add an explicit catch-all HTTPRoute rule with path prefix \"/\" instead."),
	nginxAnnotationPrefix + "service-upstream": unsupportedNginxAnnotation(
		"Gateway API always routes through the Service; this annotation has no effect to port."),
	nginxAnnotationPrefix + "upstream-vhost": unsupportedNginxAnnotation(
		"Gateway API filters cannot rewrite the Host header; use your implementation's " +
			"traffic policy CRD instead."),
	nginxAnnotationPrefix + "ssl-passthrough": unsupportedNginxAnnotation(
		"Configure TLS passthrough on the Gateway listener (protocol: TLS) instead."),
}

// RewriteTranslator maps nginx's rewrite-target onto a URLRewrite filter.
type RewriteTranslator struct{}

func (t *RewriteTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	if value == "" {
		return nil, nil
	}
	if strings.ContainsAny(value, "$()") {
		return nil, &Issue{
			Severity: SeverityWarning,
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

// SSLRedirectTranslator maps nginx's (force-)ssl-redirect onto a scheme
// redirect. A "false" value needs no filter: not redirecting is already the
// default HTTPRoute behaviour.
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

// RedirectTranslator maps nginx's permanent-redirect and temporal-redirect
// onto a RequestRedirect filter at the given status code.
type RedirectTranslator struct {
	StatusCode int
}

func (t *RedirectTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	if value == "" {
		return nil, nil
	}
	target, err := url.Parse(value)
	if err != nil {
		return nil, &Issue{
			Severity:       SeverityWarning,
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

func (t *AppRootTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	if value == "" {
		return nil, nil
	}
	return []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
				StatusCode: ptr.To(302),
				Path: &gatewayv1.HTTPPathModifier{
					Type:            gatewayv1.FullPathHTTPPathModifier,
					ReplaceFullPath: ptr.To(value),
				},
			},
		}}, &Issue{
			Severity: SeverityInfo,
			Message: fmt.Sprintf("app-root=%s was approximated as a redirect on every path "+
				"in this rule, not just \"/\"", value),
			Recommendation: "Split the root path into its own HTTPRoute rule if this is too broad.",
		}
}

// XForwardedPrefixTranslator maps nginx's x-forwarded-prefix onto a request
// header addition.
type XForwardedPrefixTranslator struct{}

func (t *XForwardedPrefixTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
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

func (t *RateLimitTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: "Express rate limiting with your implementation's policy CRD.",
	}
}

// AuthTranslator reports that external auth needs an out-of-band policy.
type AuthTranslator struct{}

func (t *AuthTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: "Express authentication with your implementation's policy CRD.",
	}
}

// unsupportedAnnotation reports that an annotation has no portable Gateway
// API equivalent, with vendor-specific guidance on how to reproduce it.
type unsupportedAnnotation struct {
	recommendation string
}

func (t *unsupportedAnnotation) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: t.recommendation,
	}
}

// unsupportedNginxAnnotation builds a Translator for an nginx annotation that
// has no safe automatic translation, so it still surfaces a specific,
// actionable warning instead of the generic "no registered translator" one.
func unsupportedNginxAnnotation(recommendation string) Translator {
	return &unsupportedAnnotation{recommendation: recommendation}
}

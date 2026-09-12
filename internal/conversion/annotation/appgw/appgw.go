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

// Package appgw translates Azure Application Gateway Ingress Controller
// (AGIC) annotations into Gateway API filters, or an explicit warning where
// there's no portable equivalent.
package appgw

import (
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/thev1ndu/transfergw/internal/conversion/translator"
)

// Prefix is shared by every AGIC annotation.
//
// This is distinct from an AKS cluster simply running ingress-nginx, which
// the nginx package already covers regardless of cloud provider - AGIC is
// the Azure-specific ingress controller, identified by this prefix.
const Prefix = "appgw.ingress.kubernetes.io/"

// Translators maps every AGIC annotation this engine knows about to the
// Translator that handles it, following the same pattern as the nginx
// package.
//
// AGIC has no per-Ingress request-rate-limit annotation: Application Gateway
// rate limiting is done via a WAF_v2 policy or Azure Front Door, not
// something an Ingress annotation can express, so there is deliberately no
// entry here for it.
var Translators = map[string]translator.Translator{
	// --- Have a portable Gateway API filter -----------------------------
	Prefix + "ssl-redirect":        &translator.SSLRedirectTranslator{},
	Prefix + "backend-path-prefix": &BackendPathPrefixTranslator{},

	// --- Set a real HTTPRouteRule field (session persistence, timeouts) -
	Prefix + "cookie-based-affinity": &translator.CookieSessionPersistenceTranslator{},
	Prefix + "request-timeout":       &translator.RequestTimeoutTranslator{},

	// --- No portable equivalent: reported, not silently dropped ---------
	Prefix + "backend-hostname": translator.Unsupported(
		"Gateway API filters cannot rewrite the Host header; use your implementation's " +
			"traffic policy CRD instead."),
	// backend-protocol: GRPC/grpc is handled structurally, before this map is
	// even consulted - see conversion.isGRPCBackend/convertGRPCRoute, which
	// produce a GRPCRoute instead of an HTTPRoute. This entry only fires for
	// every other value.
	Prefix + "backend-protocol": translator.Unsupported(
		"Set the backend Service port's appProtocol field instead."),

	Prefix + "appgw-ssl-certificate": translator.Unsupported(
		"TLS certificates apply to the Gateway listener, not the HTTPRoute; " +
			"configure certificateRefs on the Gateway resource instead."),
	Prefix + "appgw-ssl-profile": translator.Unsupported(
		"TLS policy applies to the Gateway listener, not the HTTPRoute; " +
			"configure it on the Gateway resource instead."),
	Prefix + "appgw-trusted-root-certificate": translator.Unsupported(
		"TLS trust applies to the Gateway listener, not the HTTPRoute; " +
			"configure it on the Gateway resource instead."),

	Prefix + "health-probe-hostname": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "health-probe-port": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "health-probe-path": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "health-probe-status-codes": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "health-probe-interval": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "health-probe-timeout": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "health-probe-unhealthy-threshold": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),

	// cookie-based-affinity-distinct-name asks for a per-Ingress-unique cookie
	// name, which needs the value of the sibling cookie-based-affinity
	// annotation to combine correctly - out of reach for a single-annotation
	// Translator, so this stays unsupported.
	Prefix + "cookie-based-affinity-distinct-name": translator.Unsupported(
		"Use your implementation's session-affinity policy CRD instead, or set a fixed " +
			"sessionName directly on the HTTPRoute's sessionPersistence field."),

	Prefix + "connection-draining": translator.Unsupported(
		"Use your implementation's traffic policy CRD instead."),
	Prefix + "connection-draining-timeout": translator.Unsupported(
		"Use your implementation's traffic policy CRD instead."),

	Prefix + "use-private-ip": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure the Gateway's listener/load balancer visibility instead."),
	Prefix + "override-frontend-port": translator.Unsupported(
		"This is a Gateway listener port concern, not an HTTPRoute one; " +
			"configure the Gateway's listener port instead."),

	Prefix + "waf-policy-for-path": translator.Unsupported(
		"Use your implementation's WAF/security policy CRD instead."),

	Prefix + "rewrite-rule-set": translator.Unsupported(
		"A rewrite rule set can express arbitrary header/URL rewrites that don't map " +
			"to a single filter; reproduce the specific rule(s) with HTTPRoute " +
			"URLRewrite/RequestHeaderModifier filters instead."),
	Prefix + "rewrite-rule-set-custom-resource": translator.Unsupported(
		"A rewrite rule set can express arbitrary header/URL rewrites that don't map " +
			"to a single filter; reproduce the specific rule(s) with HTTPRoute " +
			"URLRewrite/RequestHeaderModifier filters instead."),

	Prefix + "hostname-extension": translator.Unsupported(
		"Add the extra hostname(s) directly to the generated HTTPRoute's " +
			"spec.hostnames instead."),
}

// BackendPathPrefixTranslator maps AGIC's backend-path-prefix onto a
// URLRewrite filter. Unlike nginx's rewrite-target, this value is always a
// plain literal path (AGIC has no capture-group rewrite syntax), so it needs
// no additional validation before translating.
type BackendPathPrefixTranslator struct{}

func (t *BackendPathPrefixTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *translator.Issue) {
	if value == "" {
		return nil, nil
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

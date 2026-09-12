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
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// appgwAnnotationPrefix is shared by every Azure Application Gateway Ingress
// Controller (AGIC) annotation.
//
// This is distinct from an AKS cluster simply running ingress-nginx, which
// nginxTranslators already covers regardless of cloud provider - AGIC is the
// Azure-specific ingress controller, identified by this prefix.
const appgwAnnotationPrefix = "appgw.ingress.kubernetes.io/"

// appgwTranslators maps every AGIC annotation this engine knows about to the
// Translator that handles it, following the same pattern as nginxTranslators.
//
// AGIC has no per-Ingress request-rate-limit annotation: Application Gateway
// rate limiting is done via a WAF_v2 policy or Azure Front Door, not
// something an Ingress annotation can express, so there is deliberately no
// entry here for it.
var appgwTranslators = map[string]Translator{
	// --- Have a portable Gateway API filter -----------------------------
	appgwAnnotationPrefix + "ssl-redirect":        &SSLRedirectTranslator{},
	appgwAnnotationPrefix + "backend-path-prefix": &BackendPathPrefixTranslator{},

	// --- No portable equivalent: reported, not silently dropped ---------
	appgwAnnotationPrefix + "backend-hostname": unsupportedAnnotationTranslator(
		"Gateway API filters cannot rewrite the Host header; use your implementation's " +
			"traffic policy CRD instead."),
	appgwAnnotationPrefix + "backend-protocol": unsupportedAnnotationTranslator(
		"Set the backend Service port's appProtocol field instead."),
	appgwAnnotationPrefix + "request-timeout": unsupportedAnnotationTranslator(
		"Set backend timeouts with HTTPRouteRule.timeouts.backendRequest instead."),

	appgwAnnotationPrefix + "appgw-ssl-certificate": unsupportedAnnotationTranslator(
		"TLS certificates apply to the Gateway listener, not the HTTPRoute; " +
			"configure certificateRefs on the Gateway resource instead."),
	appgwAnnotationPrefix + "appgw-ssl-profile": unsupportedAnnotationTranslator(
		"TLS policy applies to the Gateway listener, not the HTTPRoute; " +
			"configure it on the Gateway resource instead."),
	appgwAnnotationPrefix + "appgw-trusted-root-certificate": unsupportedAnnotationTranslator(
		"TLS trust applies to the Gateway listener, not the HTTPRoute; " +
			"configure it on the Gateway resource instead."),

	appgwAnnotationPrefix + "health-probe-hostname": unsupportedAnnotationTranslator(
		"Use your implementation's health-check policy CRD instead."),
	appgwAnnotationPrefix + "health-probe-port": unsupportedAnnotationTranslator(
		"Use your implementation's health-check policy CRD instead."),
	appgwAnnotationPrefix + "health-probe-path": unsupportedAnnotationTranslator(
		"Use your implementation's health-check policy CRD instead."),
	appgwAnnotationPrefix + "health-probe-status-codes": unsupportedAnnotationTranslator(
		"Use your implementation's health-check policy CRD instead."),
	appgwAnnotationPrefix + "health-probe-interval": unsupportedAnnotationTranslator(
		"Use your implementation's health-check policy CRD instead."),
	appgwAnnotationPrefix + "health-probe-timeout": unsupportedAnnotationTranslator(
		"Use your implementation's health-check policy CRD instead."),
	appgwAnnotationPrefix + "health-probe-unhealthy-threshold": unsupportedAnnotationTranslator(
		"Use your implementation's health-check policy CRD instead."),

	appgwAnnotationPrefix + "cookie-based-affinity": unsupportedAnnotationTranslator(
		"Use your implementation's session-affinity policy CRD instead."),
	appgwAnnotationPrefix + "cookie-based-affinity-distinct-name": unsupportedAnnotationTranslator(
		"Use your implementation's session-affinity policy CRD instead."),

	appgwAnnotationPrefix + "connection-draining": unsupportedAnnotationTranslator(
		"Use your implementation's traffic policy CRD instead."),
	appgwAnnotationPrefix + "connection-draining-timeout": unsupportedAnnotationTranslator(
		"Use your implementation's traffic policy CRD instead."),

	appgwAnnotationPrefix + "use-private-ip": unsupportedAnnotationTranslator(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure the Gateway's listener/load balancer visibility instead."),
	appgwAnnotationPrefix + "override-frontend-port": unsupportedAnnotationTranslator(
		"This is a Gateway listener port concern, not an HTTPRoute one; " +
			"configure the Gateway's listener port instead."),

	appgwAnnotationPrefix + "waf-policy-for-path": unsupportedAnnotationTranslator(
		"Use your implementation's WAF/security policy CRD instead."),

	appgwAnnotationPrefix + "rewrite-rule-set": unsupportedAnnotationTranslator(
		"A rewrite rule set can express arbitrary header/URL rewrites that don't map " +
			"to a single filter; reproduce the specific rule(s) with HTTPRoute " +
			"URLRewrite/RequestHeaderModifier filters instead."),
	appgwAnnotationPrefix + "rewrite-rule-set-custom-resource": unsupportedAnnotationTranslator(
		"A rewrite rule set can express arbitrary header/URL rewrites that don't map " +
			"to a single filter; reproduce the specific rule(s) with HTTPRoute " +
			"URLRewrite/RequestHeaderModifier filters instead."),

	appgwAnnotationPrefix + "hostname-extension": unsupportedAnnotationTranslator(
		"Add the extra hostname(s) directly to the generated HTTPRoute's " +
			"spec.hostnames instead."),
}

// BackendPathPrefixTranslator maps AGIC's backend-path-prefix onto a
// URLRewrite filter. Unlike nginx's rewrite-target, this value is always a
// plain literal path (AGIC has no capture-group rewrite syntax), so it needs
// no additional validation before translating.
type BackendPathPrefixTranslator struct{}

func (t *BackendPathPrefixTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
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

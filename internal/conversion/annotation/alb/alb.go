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

// Package alb translates AWS Load Balancer Controller (ALB) Ingress
// annotations into Gateway API filters, or an explicit warning where there's
// no portable equivalent.
//
// Unlike nginx or AGIC, almost none of ALB's annotations describe per-request
// HTTP behaviour: they configure the load balancer and target group
// themselves (scheme, subnets, health checks, TLS, WAF, auth), which are
// Gateway/Service/implementation-policy concerns in Gateway API, not
// something an HTTPRouteRule filter can express. So this package is mostly
// explicit guidance rather than real filters - that's a property of ALB's
// annotation surface, not a gap in this translator.
package alb

import (
	"github.com/thev1ndu/transfergw/internal/conversion/translator"
)

// Prefix is shared by every AWS Load Balancer Controller annotation.
const Prefix = "alb.ingress.kubernetes.io/"

// Translators maps every ALB annotation this engine knows about to the
// Translator that handles it, following the same pattern as the nginx and
// appgw packages.
//
// Dynamic per-action/per-condition keys (actions.<name>, conditions.<name>)
// aren't listed here: their annotation key includes a user-chosen action
// name, so they can't be registered as static map entries. An Ingress using
// one still gets a "no registered translator" warning from the engine
// because it falls under the alb.ingress.kubernetes.io/ prefix, rather than
// being silently dropped.
var Translators = map[string]translator.Translator{
	// backend-protocol: GRPC is handled structurally, before this map is even
	// consulted - see conversion.isGRPCBackend/convertGRPCRoute, which produce
	// a GRPCRoute instead of an HTTPRoute. This entry only fires for every
	// other value.
	Prefix + "backend-protocol": translator.Unsupported(
		"Set the backend Service port's appProtocol field instead."),
	Prefix + "backend-protocol-version": translator.Unsupported(
		"Set the backend Service port's appProtocol field instead."),

	// --- Load balancer / listener: a Gateway concern, not an HTTPRoute one --
	Prefix + "scheme": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure the Gateway's listener/load balancer visibility instead."),
	Prefix + "ip-address-type": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource instead."),
	Prefix + "subnets": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),
	Prefix + "security-groups": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),
	Prefix + "manage-backend-security-group-rules": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),
	Prefix + "customer-owned-ipv4-pool": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),
	Prefix + "load-balancer-name": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),
	Prefix + "load-balancer-attributes": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),
	Prefix + "group.name": translator.Unsupported(
		"Sharing one load balancer across Ingresses is a Gateway-level concern; " +
			"point multiple TransferGWs at the same generated Gateway instead."),
	Prefix + "group.order": translator.Unsupported(
		"Sharing one load balancer across Ingresses is a Gateway-level concern; " +
			"point multiple TransferGWs at the same generated Gateway instead."),
	Prefix + "inbound-cidrs": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),
	Prefix + "listen-ports": translator.Unsupported(
		"This is a Gateway listener port concern, not an HTTPRoute one; " +
			"configure the Gateway's listener port(s) instead."),
	Prefix + "tags": translator.Unsupported(
		"This is a Gateway/infrastructure-level concern, not an HTTPRoute one; " +
			"configure it on the Gateway resource or its controller's infrastructure CRD instead."),

	// --- TLS: applies to the Gateway listener, not the HTTPRoute -----------
	Prefix + "certificate-arn": translator.Unsupported(
		"TLS certificates apply to the Gateway listener, not the HTTPRoute; " +
			"configure certificateRefs on the Gateway resource instead."),
	Prefix + "ssl-policy": translator.Unsupported(
		"TLS policy applies to the Gateway listener, not the HTTPRoute; " +
			"configure it on the Gateway resource instead."),
	Prefix + "ssl-negotiation-policy": translator.Unsupported(
		"TLS policy applies to the Gateway listener, not the HTTPRoute; " +
			"configure it on the Gateway resource instead."),

	// --- WAF / Shield: infrastructure-level security, not an HTTPRoute one -
	Prefix + "wafv2-acl-arn": translator.Unsupported(
		"Use your implementation's WAF/security policy CRD instead."),
	Prefix + "waf-acl-id": translator.Unsupported(
		"Use your implementation's WAF/security policy CRD instead."),
	Prefix + "shield-advanced-protection": translator.Unsupported(
		"Use your implementation's WAF/security policy CRD instead."),

	// --- Target group: a Service/backend concern, not an HTTPRoute one -----
	Prefix + "target-type": translator.Unsupported(
		"This is a Service/backend-level concern, not an HTTPRoute one; " +
			"configure it on the backend Service or its controller's infrastructure CRD instead."),
	Prefix + "target-group-attributes": translator.Unsupported(
		"This is a Service/backend-level concern, not an HTTPRoute one; " +
			"configure it on the backend Service or its controller's infrastructure CRD instead."),
	Prefix + "target-node-labels": translator.Unsupported(
		"This is a Service/backend-level concern, not an HTTPRoute one; " +
			"configure it on the backend Service or its controller's infrastructure CRD instead."),

	// --- Health checks: no core Gateway API health-check field -------------
	Prefix + "healthcheck-protocol": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "healthcheck-port": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "healthcheck-path": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "healthcheck-interval-seconds": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "healthcheck-timeout-seconds": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "healthy-threshold-count": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "unhealthy-threshold-count": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),
	Prefix + "success-codes": translator.Unsupported(
		"Use your implementation's health-check policy CRD instead."),

	// --- Auth: needs an authentication filter/policy CRD, not a core filter -
	Prefix + "auth-type": translator.Unsupported(
		"Use your implementation's authentication policy CRD (e.g. an ExtAuth filter) instead."),
	Prefix + "auth-idp-cognito": translator.Unsupported(
		"Use your implementation's authentication policy CRD (e.g. an ExtAuth filter) instead."),
	Prefix + "auth-idp-oidc": translator.Unsupported(
		"Use your implementation's authentication policy CRD (e.g. an ExtAuth filter) instead."),
	Prefix + "auth-scope": translator.Unsupported(
		"Use your implementation's authentication policy CRD (e.g. an ExtAuth filter) instead."),
	Prefix + "auth-session-cookie": translator.Unsupported(
		"Use your implementation's authentication policy CRD (e.g. an ExtAuth filter) instead."),
	Prefix + "auth-session-timeout": translator.Unsupported(
		"Use your implementation's authentication policy CRD (e.g. an ExtAuth filter) instead."),
	Prefix + "auth-on-unauthenticated-request": translator.Unsupported(
		"Use your implementation's authentication policy CRD (e.g. an ExtAuth filter) instead."),
}

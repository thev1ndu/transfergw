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
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/thev1ndu/transfergw/internal/conversion/annotation/alb"
	"github.com/thev1ndu/transfergw/internal/conversion/annotation/appgw"
	"github.com/thev1ndu/transfergw/internal/conversion/annotation/nginx"
)

// grpcBackendProtocolKeys lists every vendor's backend-protocol annotation.
// AGIC has no rate-limit annotation but does have this one; nginx has both;
// ALB has this plus backend-protocol-version, which only matters once
// GRPC is already selected here, so it isn't part of this detection.
var grpcBackendProtocolKeys = []string{
	nginx.Prefix + "backend-protocol",
	appgw.Prefix + "backend-protocol",
	alb.Prefix + "backend-protocol",
}

// isGRPCBackend reports whether ing is marked for gRPC backends via either
// vendor's backend-protocol annotation.
func isGRPCBackend(ing *networkingv1.Ingress) bool {
	for _, key := range grpcBackendProtocolKeys {
		if strings.EqualFold(ing.Annotations[key], "GRPC") {
			return true
		}
	}
	return false
}

// convertGRPCRoute builds a GRPCRoute for an Ingress marked for gRPC
// backends, instead of the usual HTTPRoute.
//
// Every rule matches unconditionally (no GRPCRouteMatch at all, which by
// spec means "match every gRPC request") rather than approximating the
// Ingress's path as a gRPC service/method match: the Ingress spec was never
// gRPC-aware to begin with, so there's no real service/method information to
// preserve, only a URL path that happens to look similar. Reporting the path
// as-is via GRPCRouteMatch would claim a precision the source data never had.
func convertGRPCRoute(ing *networkingv1.Ingress, opts Options) *Result {
	res := &Result{}

	hostnames := map[string]struct{}{}
	var rules []gatewayv1.GRPCRouteRule

	for _, rule := range ing.Spec.Rules {
		if rule.Host != "" {
			hostnames[rule.Host] = struct{}{}
		}
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			backend, issue := convertBackend(ing, path, opts.PortResolver)
			if issue != nil {
				res.Issues = append(res.Issues, *issue)
				continue
			}

			if path.Path != "" && path.Path != "/" {
				res.Issues = append(res.Issues, Issue{
					Ingress:  key(ing),
					Severity: SeverityInfo,
					Message: fmt.Sprintf("path %q was not preserved on the generated GRPCRoute: "+
						"gRPC routes match by service/method, not URL path, and the Ingress spec "+
						"carries no such information; this rule matches every gRPC call to the host",
						path.Path),
					Recommendation: "Add a method match directly on the GRPCRoute if you need to " +
						"split traffic by gRPC service or method.",
				})
			}

			rules = append(rules, gatewayv1.GRPCRouteRule{
				BackendRefs: []gatewayv1.GRPCBackendRef{{BackendRef: backend.BackendRef}},
			})
		}
	}

	if ing.Spec.DefaultBackend != nil {
		res.Issues = append(res.Issues, Issue{
			Ingress:  key(ing),
			Severity: SeverityWarning,
			Message:  "spec.defaultBackend has no Gateway API equivalent and was not converted",
			Recommendation: "Add an explicit catch-all rule to the generated GRPCRoute if you " +
				"need this behaviour.",
		})
	}

	if len(rules) == 0 {
		res.Issues = append(res.Issues, Issue{
			Ingress:        key(ing),
			Severity:       SeverityError,
			Message:        "Ingress produced no convertible gRPC rules",
			Recommendation: "Ensure at least one rule defines an http path with a Service backend.",
		})
		return res
	}

	parent := gatewayv1.ParentReference{Name: gatewayv1.ObjectName(opts.GatewayName)}
	if opts.GatewayNamespace != "" && opts.GatewayNamespace != ing.Namespace {
		parent.Namespace = ptr.To(gatewayv1.Namespace(opts.GatewayNamespace))
	}

	route := &gatewayv1.GRPCRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
		},
		Spec: gatewayv1.GRPCRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parent},
			},
			Rules: rules,
		},
	}
	for _, h := range sortedKeys(hostnames) {
		route.Spec.Hostnames = append(route.Spec.Hostnames, gatewayv1.Hostname(h))
	}

	res.GRPCRoute = route
	return res
}

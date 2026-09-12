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
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// convertPath maps an Ingress path match onto an HTTPRoute path match.
func convertPath(ing *networkingv1.Ingress, p networkingv1.HTTPIngressPath) (gatewayv1.HTTPRouteMatch, *Issue) {
	value := p.Path
	if value == "" {
		value = "/"
	}

	pathType := networkingv1.PathTypePrefix
	if p.PathType != nil {
		pathType = *p.PathType
	}

	switch pathType {
	case networkingv1.PathTypeExact:
		return matchOf(gatewayv1.PathMatchExact, value), nil

	case networkingv1.PathTypePrefix:
		return matchOf(gatewayv1.PathMatchPathPrefix, value), nil

	case networkingv1.PathTypeImplementationSpecific:
		if looksLikeRegex(value) {
			return matchOf(gatewayv1.PathMatchPathPrefix, regexPrefix(value)), &Issue{
				Ingress:  key(ing),
				Severity: SeverityWarning,
				Message: fmt.Sprintf("path %q uses ImplementationSpecific regex matching, "+
					"approximated as a prefix match on %q", value, regexPrefix(value)),
				Recommendation: "Gateway API core matching has no regex path type; verify the " +
					"approximation or use an implementation-specific route.",
			}
		}
		return matchOf(gatewayv1.PathMatchPathPrefix, value), &Issue{
			Ingress:        key(ing),
			Severity:       SeverityInfo,
			Message:        fmt.Sprintf("path %q uses ImplementationSpecific, treated as a prefix match", value),
			Recommendation: "Set pathType explicitly to Prefix or Exact to remove this ambiguity.",
		}

	default:
		return matchOf(gatewayv1.PathMatchPathPrefix, value), &Issue{
			Ingress:        key(ing),
			Severity:       SeverityWarning,
			Message:        fmt.Sprintf("unknown pathType %q, defaulted to a prefix match", pathType),
			Recommendation: "Use one of Exact, Prefix or ImplementationSpecific.",
		}
	}
}

func matchOf(t gatewayv1.PathMatchType, value string) gatewayv1.HTTPRouteMatch {
	return gatewayv1.HTTPRouteMatch{
		Path: &gatewayv1.HTTPPathMatch{
			Type:  ptr.To(t),
			Value: ptr.To(value),
		},
	}
}

// convertBackend maps an Ingress backend onto an HTTPRoute backendRef.
//
// resolvePort is consulted only when the backend references its port by name:
// HTTPBackendRef requires a port number, which the Ingress itself may not
// carry. Passing nil preserves the older behaviour of failing outright on a
// named port.
func convertBackend(
	ing *networkingv1.Ingress,
	p networkingv1.HTTPIngressPath,
	resolvePort PortResolver,
) (*gatewayv1.HTTPBackendRef, *Issue) {
	svc := p.Backend.Service
	if svc == nil {
		return nil, &Issue{
			Ingress:        key(ing),
			Severity:       SeverityError,
			Message:        fmt.Sprintf("path %q uses a resource backend, which has no Gateway API equivalent", p.Path),
			Recommendation: "Point the path at a Service backend.",
		}
	}

	ref := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Name: gatewayv1.ObjectName(svc.Name),
			},
		},
	}

	switch {
	case svc.Port.Number != 0:
		ref.Port = ptr.To(gatewayv1.PortNumber(svc.Port.Number))
		return &ref, nil

	case svc.Port.Name != "":
		if resolvePort == nil {
			return nil, &Issue{
				Ingress:  key(ing),
				Severity: SeverityError,
				Message: fmt.Sprintf("backend Service %q is referenced by port name %q; "+
					"HTTPRoute backendRefs require a port number", svc.Name, svc.Port.Name),
				Recommendation: "Reference the Service port by number in the Ingress.",
			}
		}
		n, err := resolvePort(ing.Namespace, svc.Name, svc.Port.Name)
		if err != nil {
			return nil, &Issue{
				Ingress:  key(ing),
				Severity: SeverityError,
				Message: fmt.Sprintf("backend Service %q port %q could not be resolved: %v",
					svc.Name, svc.Port.Name, err),
				Recommendation: "Confirm the referenced Service exists and has a port with that name.",
			}
		}
		ref.Port = ptr.To(gatewayv1.PortNumber(n))
		return &ref, nil

	default:
		return nil, &Issue{
			Ingress:        key(ing),
			Severity:       SeverityError,
			Message:        fmt.Sprintf("backend Service %q specifies no port", svc.Name),
			Recommendation: "Set spec.rules[].http.paths[].backend.service.port.number.",
		}
	}
}

func looksLikeRegex(p string) bool {
	return strings.ContainsAny(p, "*+?()[]{}|^$\\")
}

// regexPrefix returns the longest leading run of whole path segments that
// contain no regex metacharacters. Working a segment at a time avoids cutting
// mid-segment: "/api/v1/.*" yields "/api/v1", not "/api/v1/.".
//
// "." counts as a metacharacter here because this is only reached once
// looksLikeRegex has confirmed the path is a regex, so a literal path such as
// "/v1.0/api" never gets truncated.
func regexPrefix(p string) string {
	const meta = ".*+?()[]{}|^$\\"

	var kept []string
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			continue
		}
		if strings.ContainsAny(seg, meta) {
			break
		}
		kept = append(kept, seg)
	}

	if len(kept) == 0 {
		return "/"
	}
	return "/" + strings.Join(kept, "/")
}

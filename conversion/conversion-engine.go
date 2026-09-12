/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package conversion translates Kubernetes Ingress resources into Gateway API
// resources.
package conversion

import (
	"fmt"
	"sort"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Severity levels reported on a ConversionResult.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Options controls how a single Ingress is converted.
type Options struct {
	// GatewayName is the Gateway the generated HTTPRoutes attach to.
	GatewayName string

	// GatewayNamespace is the namespace holding that Gateway. When it differs
	// from the Ingress namespace the parentRef is qualified, which requires a
	// ReferenceGrant or a Gateway that allows routes from other namespaces.
	GatewayNamespace string

	// AnnotationPolicy optionally narrows which annotations are translated.
	AnnotationPolicy *AnnotationPolicy
}

// AnnotationPolicy mirrors the spec's annotation handling rules.
type AnnotationPolicy struct {
	// Drop lists annotation keys to ignore entirely.
	Drop []string
}

func (p *AnnotationPolicy) dropped(key string) bool {
	if p == nil {
		return false
	}
	for _, d := range p.Drop {
		if d == key {
			return true
		}
	}
	return false
}

// Issue is a warning or error raised while converting an Ingress.
type Issue struct {
	Ingress        string
	Message        string
	Severity       string
	Recommendation string
}

// Result is the outcome of converting one Ingress.
type Result struct {
	// Route is the generated HTTPRoute, or nil when the Ingress produced no
	// usable rules.
	Route *gatewayv1.HTTPRoute

	// Issues collects anything the caller should surface on status.
	Issues []Issue
}

// Failed reports whether any issue was severe enough to block the conversion.
func (r *Result) Failed() bool {
	for _, i := range r.Issues {
		if i.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Translator converts a single Ingress annotation into HTTPRoute filters.
// Returning a nil filter slice with a non-nil issue means the annotation has no
// portable Gateway API equivalent.
type Translator interface {
	Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue)
}

// Engine converts Ingress resources using a set of annotation translators.
type Engine struct {
	translators map[string]Translator
}

// NewEngine returns an Engine preloaded with the built-in translators.
func NewEngine() *Engine {
	e := &Engine{translators: make(map[string]Translator)}
	e.RegisterTranslator("nginx.ingress.kubernetes.io/rewrite-target", &RewriteTranslator{})
	e.RegisterTranslator("nginx.ingress.kubernetes.io/limit-rps", &RateLimitTranslator{})
	e.RegisterTranslator("nginx.ingress.kubernetes.io/auth-type", &AuthTranslator{})
	e.RegisterTranslator("nginx.ingress.kubernetes.io/auth-url", &AuthTranslator{})
	e.RegisterTranslator("cert-manager.io/cluster-issuer", &CertManagerTranslator{})
	e.RegisterTranslator("cert-manager.io/issuer", &CertManagerTranslator{})
	return e
}

// RegisterTranslator installs a translator for an annotation key.
func (e *Engine) RegisterTranslator(annotation string, t Translator) {
	e.translators[annotation] = t
}

// ConvertIngress translates an Ingress into a single HTTPRoute.
//
// Each Ingress rule becomes one or more HTTPRoute rules; hostnames are
// collected across rules. Paths without a backend Service, and rules the
// Gateway API cannot express, are reported as issues rather than silently
// dropped.
func (e *Engine) ConvertIngress(ing *networkingv1.Ingress, opts Options) *Result {
	res := &Result{}
	if ing == nil {
		return res
	}

	filters, filterIssues := e.convertAnnotations(ing, opts.AnnotationPolicy)
	res.Issues = append(res.Issues, filterIssues...)

	hostnames := map[string]struct{}{}
	var rules []gatewayv1.HTTPRouteRule

	for _, rule := range ing.Spec.Rules {
		if rule.Host != "" {
			hostnames[rule.Host] = struct{}{}
		}
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			backend, issue := convertBackend(ing, path)
			if issue != nil {
				res.Issues = append(res.Issues, *issue)
				continue
			}
			match, matchIssue := convertPath(ing, path)
			if matchIssue != nil {
				res.Issues = append(res.Issues, *matchIssue)
			}
			r := gatewayv1.HTTPRouteRule{
				Matches:     []gatewayv1.HTTPRouteMatch{match},
				BackendRefs: []gatewayv1.HTTPBackendRef{*backend},
			}
			if len(filters) > 0 {
				r.Filters = filters
			}
			rules = append(rules, r)
		}
	}

	if ing.Spec.DefaultBackend != nil {
		res.Issues = append(res.Issues, Issue{
			Ingress:  key(ing),
			Severity: SeverityWarning,
			Message:  "spec.defaultBackend has no Gateway API equivalent and was not converted",
			Recommendation: "Add an explicit catch-all rule with path prefix \"/\" to the " +
				"generated HTTPRoute if you need this behaviour.",
		})
	}

	if len(rules) == 0 {
		res.Issues = append(res.Issues, Issue{
			Ingress:        key(ing),
			Severity:       SeverityError,
			Message:        "Ingress produced no convertible rules",
			Recommendation: "Ensure at least one rule defines an http path with a Service backend.",
		})
		return res
	}

	parent := gatewayv1.ParentReference{
		Name: gatewayv1.ObjectName(opts.GatewayName),
	}
	if opts.GatewayNamespace != "" && opts.GatewayNamespace != ing.Namespace {
		parent.Namespace = ptr.To(gatewayv1.Namespace(opts.GatewayNamespace))
	}

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parent},
			},
			Rules: rules,
		},
	}

	for _, h := range sortedKeys(hostnames) {
		route.Spec.Hostnames = append(route.Spec.Hostnames, gatewayv1.Hostname(h))
	}

	if len(ing.Spec.TLS) > 0 {
		res.Issues = append(res.Issues, Issue{
			Ingress:  key(ing),
			Severity: SeverityInfo,
			Message: fmt.Sprintf("Ingress declares %d TLS block(s); certificates are configured "+
				"on the Gateway listener, not the HTTPRoute", len(ing.Spec.TLS)),
			Recommendation: "Add an HTTPS listener to the Gateway referencing the same Secret.",
		})
	}

	res.Route = route
	return res
}

// convertAnnotations runs every registered translator over the Ingress
// annotations, returning the filters they produced.
func (e *Engine) convertAnnotations(ing *networkingv1.Ingress, policy *AnnotationPolicy) ([]gatewayv1.HTTPRouteFilter, []Issue) {
	var filters []gatewayv1.HTTPRouteFilter
	var issues []Issue

	for _, k := range sortedMapKeys(ing.Annotations) {
		if policy.dropped(k) {
			continue
		}
		if t := e.translators[k]; t != nil {
			f, issue := t.Translate(k, ing.Annotations[k])
			filters = append(filters, f...)
			if issue != nil {
				issue.Ingress = key(ing)
				issues = append(issues, *issue)
			}
			continue
		}
		if strings.HasPrefix(k, "nginx.ingress.kubernetes.io/") {
			issues = append(issues, Issue{
				Ingress:        key(ing),
				Severity:       SeverityWarning,
				Message:        fmt.Sprintf("annotation %q has no registered translator", k),
				Recommendation: "Reproduce this behaviour with an implementation-specific policy, or drop it.",
			})
		}
	}
	return filters, issues
}

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
func convertBackend(ing *networkingv1.Ingress, p networkingv1.HTTPIngressPath) (*gatewayv1.HTTPBackendRef, *Issue) {
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
	case svc.Port.Name != "":
		return nil, &Issue{
			Ingress:  key(ing),
			Severity: SeverityError,
			Message: fmt.Sprintf("backend Service %q is referenced by port name %q; "+
				"HTTPRoute backendRefs require a port number", svc.Name, svc.Port.Name),
			Recommendation: "Reference the Service port by number in the Ingress.",
		}
	default:
		return nil, &Issue{
			Ingress:        key(ing),
			Severity:       SeverityError,
			Message:        fmt.Sprintf("backend Service %q specifies no port", svc.Name),
			Recommendation: "Set spec.rules[].http.paths[].backend.service.port.number.",
		}
	}

	return &ref, nil
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

// CertManagerTranslator reports that cert-manager wiring moves to the Gateway.
type CertManagerTranslator struct{}

func (t *CertManagerTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityInfo,
		Message:        fmt.Sprintf("%s=%s applies to the Gateway listener, not the HTTPRoute", key, value),
		Recommendation: "Move this annotation onto the Gateway resource.",
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

func key(ing *networkingv1.Ingress) string {
	return ing.Namespace + "/" + ing.Name
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

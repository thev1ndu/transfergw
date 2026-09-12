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

// Package conversion translates Kubernetes Ingress resources into Gateway API
// resources.
package conversion

import (
	"fmt"
	"sort"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/thev1ndu/transfergw/internal/conversion/annotation/appgw"
	"github.com/thev1ndu/transfergw/internal/conversion/annotation/certmanager"
	"github.com/thev1ndu/transfergw/internal/conversion/annotation/nginx"
	"github.com/thev1ndu/transfergw/internal/conversion/translator"
)

// Severity levels reported on a ConversionResult. Aliased from the
// annotation package so existing callers of conversion.Severity* keep
// working unchanged.
const (
	SeverityInfo    = translator.SeverityInfo
	SeverityWarning = translator.SeverityWarning
	SeverityError   = translator.SeverityError
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

	// PortResolver looks up the numeric port for a Service backend that's
	// referenced by name instead of number, so it can still convert. Optional:
	// nil means a named port fails conversion, same as before this existed.
	PortResolver PortResolver
}

// PortResolver looks up the numeric port for a named Service port.
type PortResolver func(namespace, serviceName, portName string) (int32, error)

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

// Issue is a warning or error raised while converting an Ingress. Aliased
// from the annotation package so existing callers of conversion.Issue keep
// working unchanged.
type Issue = translator.Issue

// Result is the outcome of converting one Ingress.
type Result struct {
	// Route is the generated HTTPRoute, or nil when the Ingress produced no
	// usable rules or converted to a GRPCRoute instead (see GRPCRoute).
	Route *gatewayv1.HTTPRoute

	// GRPCRoute is set instead of Route when the Ingress is marked for gRPC
	// backends (backend-protocol: GRPC, nginx or AGIC). Exactly one of Route
	// or GRPCRoute is non-nil on a successful conversion.
	GRPCRoute *gatewayv1.GRPCRoute

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
// Aliased from the annotation package so existing callers of
// conversion.Translator keep working unchanged.
type Translator = translator.Translator

// Engine converts Ingress resources using a set of annotation translators.
type Engine struct {
	translators map[string]Translator
}

// vendorRegistries lists every vendor's annotation-to-translator map.
//
// To support a new vendor, add a package under internal/conversion/annotation
// (see nginx, certmanager, appgw for the shape) and list its Translators map
// here. NewEngine never needs any other change.
var vendorRegistries = []map[string]Translator{
	nginx.Translators,
	certmanager.Translators,
	appgw.Translators,
}

// vendorPrefixes lists every vendor annotation prefix this engine knows
// about, so an unregistered annotation under a known vendor is reported as a
// gap instead of silently ignored. List a new vendor's prefix here alongside
// its entry in vendorRegistries.
var vendorPrefixes = []string{
	nginx.Prefix,
	certmanager.Prefix,
	appgw.Prefix,
}

func isKnownVendorPrefix(annotation string) bool {
	for _, prefix := range vendorPrefixes {
		if strings.HasPrefix(annotation, prefix) {
			return true
		}
	}
	return false
}

// NewEngine returns an Engine preloaded with the built-in translators.
func NewEngine() *Engine {
	e := &Engine{translators: make(map[string]Translator)}
	for _, registry := range vendorRegistries {
		for annotation, t := range registry {
			e.RegisterTranslator(annotation, t)
		}
	}
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

	// gRPC backends get their own Gateway API resource type. Ingress carries
	// no per-method gRPC routing information either way (nginx's
	// backend-protocol: GRPC only changes the proxy protocol nginx speaks to
	// the backend, not the Ingress spec), and HTTPRouteFilter/GRPCRouteFilter
	// are different types, so annotation-derived filters don't carry over -
	// this is a structurally different conversion, not a variant of the one
	// below.
	if isGRPCBackend(ing) {
		return convertGRPCRoute(ing, opts)
	}

	filters, ruleEffect, filterIssues := e.convertAnnotations(ing, opts.AnnotationPolicy)
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
			backend, issue := convertBackend(ing, path, opts.PortResolver)
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
			if ruleEffect != nil {
				r.SessionPersistence = ruleEffect.SessionPersistence
				r.Timeouts = ruleEffect.Timeouts
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
// annotations, returning the filters they produced and any HTTPRouteRule-level
// effect (session affinity, timeouts) they set - the two things a Translator
// can contribute, since some Gateway API behaviour (SessionPersistence,
// Timeouts) lives on the rule itself, not in a filter within it.
func (e *Engine) convertAnnotations(
	ing *networkingv1.Ingress,
	policy *AnnotationPolicy,
) ([]gatewayv1.HTTPRouteFilter, *translator.RuleEffect, []Issue) {
	var filters []gatewayv1.HTTPRouteFilter
	var effect *translator.RuleEffect
	var issues []Issue

	for _, k := range sortedMapKeys(ing.Annotations) {
		if policy.dropped(k) {
			continue
		}
		t := e.translators[k]
		if t == nil {
			if isKnownVendorPrefix(k) {
				issues = append(issues, Issue{
					Ingress:        key(ing),
					Severity:       SeverityWarning,
					Message:        fmt.Sprintf("annotation %q has no registered translator", k),
					Recommendation: "Reproduce this behaviour with an implementation-specific policy, or drop it.",
				})
			}
			continue
		}

		var f []gatewayv1.HTTPRouteFilter
		var issue *Issue
		if ct, ok := t.(translator.ContextualTranslator); ok {
			f, issue = ct.TranslateWithContext(k, ing.Annotations[k], ing.Annotations)
		} else {
			f, issue = t.Translate(k, ing.Annotations[k])
		}
		filters = append(filters, f...)
		if issue != nil {
			issue.Ingress = key(ing)
			issues = append(issues, *issue)
		}

		var eff *translator.RuleEffect
		var effIssue *Issue
		if cre, ok := t.(translator.ContextualRuleEffector); ok {
			eff, effIssue = cre.EffectWithContext(k, ing.Annotations[k], ing.Annotations)
		} else if effector, ok := t.(translator.RuleEffector); ok {
			eff, effIssue = effector.Effect(k, ing.Annotations[k])
		} else {
			continue
		}
		if effIssue != nil {
			effIssue.Ingress = key(ing)
			issues = append(issues, *effIssue)
		}
		effect = mergeRuleEffect(effect, eff)
	}
	return filters, effect, issues
}

// mergeRuleEffect combines two RuleEffects into one, since exactly one
// SessionPersistence and one Timeouts can end up on an HTTPRouteRule even
// when more than one annotation contributes to them (e.g. nginx's
// proxy-read-timeout and proxy-send-timeout both target backendRequest).
func mergeRuleEffect(a, b *translator.RuleEffect) *translator.RuleEffect {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	merged := &translator.RuleEffect{Timeouts: mergeTimeouts(a.Timeouts, b.Timeouts)}
	// Sorted-key iteration order makes this deterministic: whichever
	// annotation is later alphabetically wins a genuine collision, which is
	// an edge case (two different vendors' affinity annotations on one
	// Ingress) rather than something expected to happen in practice.
	if b.SessionPersistence != nil {
		merged.SessionPersistence = b.SessionPersistence
	} else {
		merged.SessionPersistence = a.SessionPersistence
	}
	return merged
}

// mergeTimeouts combines two Timeouts, taking the larger duration per field
// when both set it, so neither annotation's constraint is silently dropped.
func mergeTimeouts(a, b *gatewayv1.HTTPRouteTimeouts) *gatewayv1.HTTPRouteTimeouts {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &gatewayv1.HTTPRouteTimeouts{
		Request:        longerDuration(a.Request, b.Request),
		BackendRequest: longerDuration(a.BackendRequest, b.BackendRequest),
	}
}

func longerDuration(a, b *gatewayv1.Duration) *gatewayv1.Duration {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	da, errA := time.ParseDuration(string(*a))
	db, errB := time.ParseDuration(string(*b))
	if errA != nil {
		return b
	}
	if errB != nil {
		return a
	}
	if da >= db {
		return a
	}
	return b
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

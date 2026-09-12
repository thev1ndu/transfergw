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

// Package canary merges nginx weight-based canary Ingress pairs into one
// HTTPRoute with two weighted backendRefs, instead of converting the canary
// as a second, unrelated route.
package canary

import (
	"strconv"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/thev1ndu/transfergw/internal/conversion"
	"github.com/thev1ndu/transfergw/internal/conversion/annotation/nginx"
)

// Pairing is a canary Ingress paired with the weight nginx would give it,
// ready to fold into its primary's HTTPRoute as a second backendRef.
type Pairing struct {
	Ingress *networkingv1.Ingress
	Weight  int32
}

// Pair finds nginx weight-based canary/primary Ingress pairs among
// ingresses: a canary (canary: "true", a valid canary-weight) and a primary
// (no canary annotation) sharing the same namespace, host and single path.
//
// HTTPBackendRef.Weight already exists in Gateway API and is exactly what
// nginx's canary mechanism approximates - a canary Ingress isn't really a
// second route, it's a second weighted backend for the same route the
// primary Ingress already describes. Anything more elaborate than that exact
// shape (multiple paths/hosts, an unpaired canary, more than one candidate
// primary) falls back to the existing "no equivalent" canary warning rather
// than risk merging routes that don't actually match.
//
// Returns which Ingress name (namespace/name) is a successfully paired
// canary (to exclude from independent conversion) and, per primary Ingress
// name, the pairing to fold in after it converts normally.
func Pair(ingresses []networkingv1.Ingress) (names map[string]bool, forPrimary map[string]Pairing) {
	names = map[string]bool{}
	forPrimary = map[string]Pairing{}

	bySignature := map[string][]*networkingv1.Ingress{}
	for i := range ingresses {
		ing := &ingresses[i]
		sig, ok := routeSignature(ing)
		if !ok {
			continue
		}
		bySignature[sig] = append(bySignature[sig], ing)
	}

	for _, group := range bySignature {
		if len(group) != 2 {
			continue
		}
		var primary, canaryIng *networkingv1.Ingress
		for _, ing := range group {
			if isCanary(ing) {
				canaryIng = ing
			} else {
				primary = ing
			}
		}
		if primary == nil || canaryIng == nil {
			continue
		}
		weight, ok := canaryWeight(canaryIng)
		if !ok {
			continue
		}
		names[canaryIng.Namespace+"/"+canaryIng.Name] = true
		forPrimary[primary.Namespace+"/"+primary.Name] = Pairing{Ingress: canaryIng, Weight: weight}
	}
	return names, forPrimary
}

func isCanary(ing *networkingv1.Ingress) bool {
	return ing.Annotations[nginx.Prefix+"canary"] == "true"
}

func canaryWeight(ing *networkingv1.Ingress) (int32, bool) {
	v, ok := ing.Annotations[nginx.Prefix+"canary-weight"]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	return int32(n), true
}

// routeSignature identifies an Ingress's single host+path route, so a canary
// Ingress can be matched against its primary. Only single-host, single-path
// Ingresses are supported for pairing.
func routeSignature(ing *networkingv1.Ingress) (string, bool) {
	if len(ing.Spec.Rules) != 1 {
		return "", false
	}
	rule := ing.Spec.Rules[0]
	if rule.HTTP == nil || len(rule.HTTP.Paths) != 1 {
		return "", false
	}
	return ing.Namespace + "|" + rule.Host + "|" + rule.HTTP.Paths[0].Path, true
}

// NonCanaryIssues drops the canary/canary-weight "no core equivalent"
// warnings from a successfully-merged canary Ingress's own issues - true in
// general, but misleading for exactly the case that was just handled.
func NonCanaryIssues(issues []conversion.Issue) []conversion.Issue {
	var out []conversion.Issue
	for _, iss := range issues {
		if strings.Contains(iss.Message, nginx.Prefix+"canary") {
			continue
		}
		out = append(out, iss)
	}
	return out
}

// MergeBackend adds the canary's backendRef, from its own already-converted
// route, onto every rule of the primary's route, weighting both sides so
// traffic actually splits the way canary-weight asked for.
func MergeBackend(primaryRoute *gatewayv1.HTTPRoute, canaryRoute *gatewayv1.HTTPRoute, weight int32) {
	if len(canaryRoute.Spec.Rules) == 0 || len(canaryRoute.Spec.Rules[0].BackendRefs) == 0 {
		return
	}
	canaryBackend := canaryRoute.Spec.Rules[0].BackendRefs[0]
	canaryBackend.Weight = &weight
	primaryWeight := 100 - weight

	for i := range primaryRoute.Spec.Rules {
		for j := range primaryRoute.Spec.Rules[i].BackendRefs {
			primaryRoute.Spec.Rules[i].BackendRefs[j].Weight = &primaryWeight
		}
		primaryRoute.Spec.Rules[i].BackendRefs = append(primaryRoute.Spec.Rules[i].BackendRefs, canaryBackend)
	}
}

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

// Package overlap warns when more than one selected Ingress matches the
// exact same host+path+pathType with a different backend.
package overlap

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/conversion"
)

// entry names one Ingress/path that matched a given host+path+pathType
// signature, along with the backend it points at.
type entry struct {
	ingress string // namespace/name
	backend string // service:port
}

// Check warns when more than one selected Ingress matches the exact same
// host+path+pathType with a different backend. nginx and Gateway API
// resolve that kind of tie by different precedence rules (nginx: annotation
// priority/creation order; Gateway API: match specificity, then oldest
// resource, then name) - a migration can't guarantee the same Ingress keeps
// winning, so this is surfaced rather than left to be discovered as a
// silent routing change after cutover.
//
// canaryNames is excluded: a paired canary Ingress is expected to share its
// primary's exact host+path by design (that's what makes the pairing
// possible), and canary.Pair already resolved that relationship, so it's
// not a genuine ambiguity.
func Check(ingresses []networkingv1.Ingress, canaryNames map[string]bool) []transfergwv1beta1.ConversionIssue {
	bySignature := map[string][]entry{}

	for i := range ingresses {
		ing := &ingresses[i]
		name := ing.Namespace + "/" + ing.Name
		if canaryNames[name] {
			continue
		}
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				pathType := networkingv1.PathTypePrefix
				if path.PathType != nil {
					pathType = *path.PathType
				}
				sig := fmt.Sprintf("%s|%s|%s", rule.Host, pathType, path.Path)
				bySignature[sig] = append(bySignature[sig], entry{
					ingress: name,
					backend: backendSignature(path),
				})
			}
		}
	}

	var issues []transfergwv1beta1.ConversionIssue
	for _, entries := range bySignature {
		if len(entries) < 2 {
			continue
		}
		distinct := map[string]bool{}
		var names []string
		for _, e := range entries {
			if !distinct[e.backend] {
				distinct[e.backend] = true
			}
			names = append(names, e.ingress)
		}
		if len(distinct) < 2 {
			continue // same match, same backend on every Ingress - harmless duplication
		}
		issues = append(issues, transfergwv1beta1.ConversionIssue{
			Ingress:  names[0],
			Severity: conversion.SeverityWarning,
			Issue: fmt.Sprintf("Ingresses %v match the exact same host+path with different "+
				"backends; nginx and Gateway API resolve that ambiguity with different "+
				"precedence rules, so routing may change after migration", names),
			Recommendation: "Make the paths distinct, or confirm which backend should win " +
				"and drop the others from the selector.",
		})
	}
	return issues
}

func backendSignature(path networkingv1.HTTPIngressPath) string {
	if path.Backend.Service == nil {
		return "resource-backend"
	}
	if path.Backend.Service.Port.Number != 0 {
		return fmt.Sprintf("%s:%d", path.Backend.Service.Name, path.Backend.Service.Port.Number)
	}
	return fmt.Sprintf("%s:%s", path.Backend.Service.Name, path.Backend.Service.Port.Name)
}

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

// Package selector resolves a TransferGW's spec.selector (namespaces, label
// selector, ingress classes) against the live cluster into a concrete list
// of Ingresses.
package selector

import (
	"context"
	"fmt"
	"path"
	"sort"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
)

// SelectIngresses returns the Ingresses matching a TransferGW's selector.
//
// Exported (and taking a plain client.Client rather than a Reconciler) so
// the preview CLI (cmd/preview) and the validating webhook can run the
// exact same selection logic the controller uses, against a real cluster,
// without a manager or a persisted TransferGW object.
func SelectIngresses(
	ctx context.Context,
	c client.Client,
	sel transfergwv1beta1.SelectorSpec,
) ([]networkingv1.Ingress, error) {
	labelSelector := labels.Everything()
	if sel.IngressSelector != nil {
		s, err := metav1.LabelSelectorAsSelector(sel.IngressSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid ingressSelector: %w", err)
		}
		labelSelector = s
	}

	namespaces, err := resolveNamespaces(ctx, c, sel.Namespaces)
	if err != nil {
		return nil, err
	}

	var out []networkingv1.Ingress
	for _, ns := range namespaces {
		list := &networkingv1.IngressList{}
		if err := c.List(ctx, list,
			client.InNamespace(ns),
			client.MatchingLabelsSelector{Selector: labelSelector},
		); err != nil {
			return nil, fmt.Errorf("listing ingresses in %s: %w", ns, err)
		}
		for i := range list.Items {
			if matchesIngressClass(&list.Items[i], sel.IngressClasses) {
				out = append(out, list.Items[i])
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// resolveNamespaces expands the namespace patterns against the cluster. An
// empty pattern list means every namespace.
func resolveNamespaces(ctx context.Context, c client.Client, patterns []string) ([]string, error) {
	nsList := &corev1.NamespaceList{}
	if err := c.List(ctx, nsList); err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}

	var out []string
	for _, ns := range nsList.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			continue
		}
		if len(patterns) == 0 || matchesAnyPattern(ns.Name, patterns) {
			out = append(out, ns.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// matchesAnyPattern reports whether name matches any glob pattern.
func matchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == name {
			return true
		}
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// matchesIngressClass reports whether the Ingress belongs to one of the
// given ingress classes (spec.ingressClassName, falling back to the legacy
// kubernetes.io/ingress.class annotation). An empty class list matches
// everything.
func matchesIngressClass(ing *networkingv1.Ingress, classes []string) bool {
	if len(classes) == 0 {
		return true
	}
	name := ""
	if ing.Spec.IngressClassName != nil {
		name = *ing.Spec.IngressClassName
	} else if v, ok := ing.Annotations["kubernetes.io/ingress.class"]; ok {
		name = v
	}
	for _, c := range classes {
		if c == name {
			return true
		}
	}
	return false
}

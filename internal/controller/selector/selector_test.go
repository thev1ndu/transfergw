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

package selector

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestMatchesAnyPattern(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		want     bool
	}{
		{"prod", []string{"prod"}, true},
		{"prod-a", []string{"prod-*"}, true},
		{"staging", []string{"prod-*"}, false},
		{"anything", []string{"*"}, true},
		{"prod", nil, false},
	}
	for _, tt := range tests {
		if got := matchesAnyPattern(tt.name, tt.patterns); got != tt.want {
			t.Errorf("matchesAnyPattern(%q, %v) = %v, want %v", tt.name, tt.patterns, got, tt.want)
		}
	}
}

func TestMatchesIngressClass(t *testing.T) {
	withClass := &networkingv1.Ingress{
		Spec: networkingv1.IngressSpec{IngressClassName: ptr.To("nginx")},
	}
	withAnnotation := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{"kubernetes.io/ingress.class": "nginx"},
		},
	}

	if !matchesIngressClass(withClass, nil) {
		t.Error("an empty class list should match everything")
	}
	if !matchesIngressClass(withClass, []string{"nginx"}) {
		t.Error("spec.ingressClassName should match")
	}
	if matchesIngressClass(withClass, []string{"traefik"}) {
		t.Error("a different class should not match")
	}
	if !matchesIngressClass(withAnnotation, []string{"nginx"}) {
		t.Error("the legacy ingress.class annotation should match")
	}
}

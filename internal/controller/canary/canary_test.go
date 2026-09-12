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

package canary

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/thev1ndu/transfergw/internal/conversion/annotation/nginx"
)

func singlePathIngress(name, host string, annotations map[string]string) networkingv1.Ingress {
	return networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "demo", Annotations: annotations},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/",
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: name, Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
}

func TestPairFindsAMatchingPrimary(t *testing.T) {
	primary := singlePathIngress("main", "demo.test", nil)
	canaryIng := singlePathIngress("main-canary", "demo.test", map[string]string{
		nginx.Prefix + "canary":        "true",
		nginx.Prefix + "canary-weight": "20",
	})

	names, forPrimary := Pair([]networkingv1.Ingress{primary, canaryIng})

	if !names["demo/main-canary"] {
		t.Errorf("names = %v, want main-canary marked as paired", names)
	}
	pairing, ok := forPrimary["demo/main"]
	if !ok {
		t.Fatalf("no pairing found for the primary Ingress")
	}
	if pairing.Weight != 20 {
		t.Errorf("weight = %d, want 20", pairing.Weight)
	}
}

func TestPairIgnoresMismatchedHosts(t *testing.T) {
	primary := singlePathIngress("main", "demo.test", nil)
	canaryIng := singlePathIngress("main-canary", "other.test", map[string]string{
		nginx.Prefix + "canary":        "true",
		nginx.Prefix + "canary-weight": "20",
	})

	names, forPrimary := Pair([]networkingv1.Ingress{primary, canaryIng})
	if len(names) != 0 || len(forPrimary) != 0 {
		t.Errorf("expected no pairing for mismatched hosts, got %v / %v", names, forPrimary)
	}
}

func TestPairRequiresAValidWeight(t *testing.T) {
	primary := singlePathIngress("main", "demo.test", nil)
	canaryIng := singlePathIngress("main-canary", "demo.test", map[string]string{
		nginx.Prefix + "canary": "true",
		// no canary-weight
	})

	_, forPrimary := Pair([]networkingv1.Ingress{primary, canaryIng})
	if len(forPrimary) != 0 {
		t.Errorf("expected no pairing without a valid canary-weight, got %v", forPrimary)
	}
}

func TestMergeBackendWeightsBothSides(t *testing.T) {
	primaryRoute := &gatewayv1.HTTPRoute{
		Spec: gatewayv1.HTTPRouteSpec{
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{Name: "main"},
					},
				}},
			}},
		},
	}
	canaryRoute := &gatewayv1.HTTPRoute{
		Spec: gatewayv1.HTTPRouteSpec{
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{Name: "canary"},
					},
				}},
			}},
		},
	}

	MergeBackend(primaryRoute, canaryRoute, 20)

	refs := primaryRoute.Spec.Rules[0].BackendRefs
	if len(refs) != 2 {
		t.Fatalf("backendRefs = %+v, want 2 (primary + canary)", refs)
	}
	if refs[0].Name != "main" || refs[0].Weight == nil || *refs[0].Weight != 80 {
		t.Errorf("primary backendRef = %+v, want main weighted 80", refs[0])
	}
	if refs[1].Name != "canary" || refs[1].Weight == nil || *refs[1].Weight != 20 {
		t.Errorf("canary backendRef = %+v, want canary weighted 20", refs[1])
	}
}

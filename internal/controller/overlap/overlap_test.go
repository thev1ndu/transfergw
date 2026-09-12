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

package overlap

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestCheckWarnsOnSameMatchDifferentBackend(t *testing.T) {
	a := singlePathIngress("a", "demo.test", nil)
	b := singlePathIngress("b", "demo.test", nil)
	b.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "different-service"

	issues := Check([]networkingv1.Ingress{a, b}, map[string]bool{})
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want 1", issues)
	}
	if issues[0].Severity != "warning" {
		t.Errorf("severity = %q, want warning", issues[0].Severity)
	}
}

func TestCheckIgnoresIdenticalBackends(t *testing.T) {
	a := singlePathIngress("a", "demo.test", nil)
	b := singlePathIngress("b", "demo.test", nil)
	b.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "a" // same name+port as a

	issues := Check([]networkingv1.Ingress{a, b}, map[string]bool{})
	if len(issues) != 0 {
		t.Errorf("issues = %+v, want none for identical backends", issues)
	}
}

func TestCheckIgnoresDifferentPaths(t *testing.T) {
	a := singlePathIngress("a", "demo.test", nil)
	b := singlePathIngress("b", "demo.test", nil)
	b.Spec.Rules[0].HTTP.Paths[0].Path = "/other"
	b.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "different-service"

	issues := Check([]networkingv1.Ingress{a, b}, map[string]bool{})
	if len(issues) != 0 {
		t.Errorf("issues = %+v, want none for non-overlapping paths", issues)
	}
}

func TestCheckExcludesPairedCanaries(t *testing.T) {
	primary := singlePathIngress("main", "demo.test", nil)
	canaryIng := singlePathIngress("main-canary", "demo.test", nil)
	canaryIng.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name = "canary-service"

	// Same host+path+different backend as "main", but marked as an already
	// resolved canary pairing - not a genuine conflict.
	issues := Check(
		[]networkingv1.Ingress{primary, canaryIng},
		map[string]bool{"demo/main-canary": true},
	)
	if len(issues) != 0 {
		t.Errorf("issues = %+v, want none for a resolved canary pairing", issues)
	}
}

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
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
)

func TestNginxBackendProtocolGRPCProducesAGRPCRoute(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/backend-protocol": "GRPC",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.Route != nil {
		t.Error("expected no HTTPRoute for a gRPC-marked Ingress")
	}
	if res.GRPCRoute == nil {
		t.Fatal("expected a GRPCRoute")
	}
	if len(res.GRPCRoute.Spec.Rules) != 1 {
		t.Fatalf("rules = %+v, want 1", res.GRPCRoute.Spec.Rules)
	}
	if len(res.GRPCRoute.Spec.Rules[0].Matches) != 0 {
		t.Errorf("matches = %+v, want none (match every gRPC call)", res.GRPCRoute.Spec.Rules[0].Matches)
	}
	if len(res.GRPCRoute.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("backendRefs = %+v, want 1", res.GRPCRoute.Spec.Rules[0].BackendRefs)
	}
	if got := string(res.GRPCRoute.Spec.Rules[0].BackendRefs[0].Name); got != "sample-nginx" {
		t.Errorf("backend name = %q, want sample-nginx", got)
	}
}

func TestBackendProtocolGRPCIsCaseInsensitive(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"appgw.ingress.kubernetes.io/backend-protocol": "grpc",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.GRPCRoute == nil {
		t.Fatal("expected a GRPCRoute for a lowercase grpc value")
	}
}

func TestGRPCRouteWarnsWhenPathIsNotRoot(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/backend-protocol": "GRPC",
		}
		i.Spec.Rules[0].HTTP.Paths[0].Path = "/mypackage.MyService"
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.GRPCRoute == nil {
		t.Fatal("expected a GRPCRoute")
	}
	if !hasSeverity(res.Issues, SeverityInfo) {
		t.Errorf("expected an info issue about the path not being preserved, got %+v", res.Issues)
	}
}

func TestBackendProtocolNonGRPCStillProducesHTTPRoute(t *testing.T) {
	ing := ingress(func(i *networkingv1.Ingress) {
		i.Annotations = map[string]string{
			"nginx.ingress.kubernetes.io/backend-protocol": "HTTPS",
		}
	})

	res := NewEngine().ConvertIngress(ing, defaultOpts())
	if res.GRPCRoute != nil {
		t.Error("expected no GRPCRoute for a non-GRPC backend-protocol value")
	}
	if res.Route == nil {
		t.Fatal("expected a normal HTTPRoute")
	}
	if !hasSeverity(res.Issues, SeverityWarning) {
		t.Errorf("expected the usual backend-protocol warning, got %+v", res.Issues)
	}
}

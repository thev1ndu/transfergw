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

package controller

import (
	"context"
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestReconcileMergesCanaryIntoOneWeightedHTTPRoute(t *testing.T) {
	migration := testMigration()
	primary := testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"})
	canary := testIngress("sample-nginx-canary", "demo", map[string]string{"migrate": "true"})
	canary.Spec.Rules[0].Host = primary.Spec.Rules[0].Host // must share the primary's host+path to pair
	canary.Annotations = map[string]string{
		"nginx.ingress.kubernetes.io/canary":        "true",
		"nginx.ingress.kubernetes.io/canary-weight": "20",
	}

	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		primary, canary, migration,
	)

	reconcileOnce(t, r, migration)

	var routes gatewayv1.HTTPRouteList
	if err := c.List(context.Background(), &routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 1 {
		t.Fatalf("routes created = %d, want 1 (canary merged into the primary's route)", len(routes.Items))
	}

	refs := routes.Items[0].Spec.Rules[0].BackendRefs
	if len(refs) != 2 {
		t.Fatalf("backendRefs = %+v, want 2 (primary + canary)", refs)
	}
	var primaryWeight, canaryWeight *int32
	for _, ref := range refs {
		switch string(ref.Name) {
		case "sample-nginx":
			primaryWeight = ref.Weight
		case "sample-nginx-canary":
			canaryWeight = ref.Weight
		}
	}
	if primaryWeight == nil || *primaryWeight != 80 {
		t.Errorf("primary weight = %v, want 80", primaryWeight)
	}
	if canaryWeight == nil || *canaryWeight != 20 {
		t.Errorf("canary weight = %v, want 20", canaryWeight)
	}

	got := readMigration(t, c, migration)
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Converted != 2 {
		t.Errorf("processed = %+v, want 2 converted (primary + merged canary)", got.Status.ProcessedStatus)
	}
}

func TestReconcileLeavesUnpairedCanaryAsNormalWarning(t *testing.T) {
	migration := testMigration()
	canary := testIngress("orphan-canary", "demo", map[string]string{"migrate": "true"})
	canary.Annotations = map[string]string{
		"nginx.ingress.kubernetes.io/canary":        "true",
		"nginx.ingress.kubernetes.io/canary-weight": "20",
	}

	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		canary, migration,
	)

	reconcileOnce(t, r, migration)

	var routes gatewayv1.HTTPRouteList
	if err := c.List(context.Background(), &routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 1 {
		t.Fatalf("routes created = %d, want 1 (converted independently, no pairing found)", len(routes.Items))
	}
	if len(routes.Items[0].Spec.Rules[0].BackendRefs) != 1 {
		t.Errorf("backendRefs = %+v, want just the canary's own backend", routes.Items[0].Spec.Rules[0].BackendRefs)
	}

	got := readMigration(t, c, migration)
	found := false
	for _, iss := range got.Status.Issues {
		if iss.Severity == "warning" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the usual canary/canary-weight warning for an unpaired canary, got %+v", got.Status.Issues)
	}
}

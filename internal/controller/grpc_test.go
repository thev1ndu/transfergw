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

func TestReconcileAppliesGRPCRouteForGRPCBackend(t *testing.T) {
	migration := testMigration()
	ing := testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"})
	ing.Annotations = map[string]string{
		"nginx.ingress.kubernetes.io/backend-protocol": "GRPC",
	}

	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		ing, migration,
	)

	reconcileOnce(t, r, migration)

	var grpcRoutes gatewayv1.GRPCRouteList
	if err := c.List(context.Background(), &grpcRoutes); err != nil {
		t.Fatalf("listing GRPCRoutes: %v", err)
	}
	if len(grpcRoutes.Items) != 1 {
		t.Fatalf("GRPCRoutes = %d, want 1", len(grpcRoutes.Items))
	}

	var httpRoutes gatewayv1.HTTPRouteList
	if err := c.List(context.Background(), &httpRoutes); err != nil {
		t.Fatalf("listing HTTPRoutes: %v", err)
	}
	if len(httpRoutes.Items) != 0 {
		t.Errorf("HTTPRoutes = %d, want 0 (this Ingress should only produce a GRPCRoute)", len(httpRoutes.Items))
	}

	got := readMigration(t, c, migration)
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Converted != 1 {
		t.Errorf("processed = %+v, want 1 converted", got.Status.ProcessedStatus)
	}
}

func TestReconcilePrunesOrphanedGRPCRoute(t *testing.T) {
	migration := testMigration()
	ing := testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"})
	ing.Annotations = map[string]string{
		"nginx.ingress.kubernetes.io/backend-protocol": "GRPC",
	}

	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		ing, migration,
	)
	reconcileOnce(t, r, migration)

	// The Ingress no longer matches the selector - unlabel it.
	updatedIngress := ing.DeepCopy()
	delete(updatedIngress.Labels, "migrate")
	if err := c.Update(context.Background(), updatedIngress); err != nil {
		t.Fatalf("updating ingress: %v", err)
	}

	reconcileOnce(t, r, migration)

	var grpcRoutes gatewayv1.GRPCRouteList
	if err := c.List(context.Background(), &grpcRoutes); err != nil {
		t.Fatalf("listing GRPCRoutes: %v", err)
	}
	if len(grpcRoutes.Items) != 0 {
		t.Errorf("GRPCRoutes = %d, want 0 (should have been pruned)", len(grpcRoutes.Items))
	}
}

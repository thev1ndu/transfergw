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

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/lifecycle"
)

type fakeHookCaller struct {
	proceed bool
	calls   []lifecycle.Event
}

func (f *fakeHookCaller) Call(_ context.Context, _ string, event lifecycle.Event) (bool, error) {
	f.calls = append(f.calls, event)
	return f.proceed, nil
}

func ensureHooks(m *transfergwv1beta1.TransferGW) *transfergwv1beta1.HooksSpec {
	if m.Spec.Lifecycle == nil {
		m.Spec.Lifecycle = &transfergwv1beta1.LifecycleSpec{}
	}
	if m.Spec.Lifecycle.Hooks == nil {
		m.Spec.Lifecycle.Hooks = &transfergwv1beta1.HooksSpec{}
	}
	return m.Spec.Lifecycle.Hooks
}

func withPreConversionHook(url string) func(*transfergwv1beta1.TransferGW) {
	return func(m *transfergwv1beta1.TransferGW) {
		ensureHooks(m).PreConversion = &transfergwv1beta1.WebhookSpec{Url: url}
	}
}

func withPreRolloutHook(url string) func(*transfergwv1beta1.TransferGW) {
	return func(m *transfergwv1beta1.TransferGW) {
		ensureHooks(m).PreRollout = &transfergwv1beta1.WebhookSpec{Url: url}
	}
}

func TestReconcileHoldsOnUnapprovedPreConversionHook(t *testing.T) {
	migration := testMigration(withPreConversionHook("https://hooks.example/pre-conversion"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.HookCaller = &fakeHookCaller{proceed: false}

	res := reconcileOnce(t, r, migration)
	if res.RequeueAfter == 0 {
		t.Error("requeueAfter = 0, want a retry while the hook is unapproved")
	}

	got := readMigration(t, c, migration)
	if got.Status.Phase != phasePending {
		t.Errorf("phase = %q, want %q", got.Status.Phase, phasePending)
	}
	if got.Status.ProcessedStatus != nil {
		t.Error("expected no conversion to have run while PreConversion is blocked")
	}

	var routes gatewayv1.HTTPRouteList
	if err := c.List(context.Background(), &routes); err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	if len(routes.Items) != 0 {
		t.Errorf("routes created = %d, want 0 while PreConversion is blocked", len(routes.Items))
	}
}

func TestReconcileProceedsAfterPreConversionApproves(t *testing.T) {
	migration := testMigration(withPreConversionHook("https://hooks.example/pre-conversion"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	caller := &fakeHookCaller{proceed: true}
	r.HookCaller = caller

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase == phasePending {
		t.Fatal("phase is still Pending after the hook approved")
	}
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Converted != 1 {
		t.Errorf("processed = %+v, want 1 converted", got.Status.ProcessedStatus)
	}
	if len(caller.calls) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(caller.calls))
	}
	if caller.calls[0].Stage != string(lifecycle.PreConversion) {
		t.Errorf("stage = %q, want %q", caller.calls[0].Stage, lifecycle.PreConversion)
	}
}

func TestReconcileDoesNotReinvokeApprovedHookSameGeneration(t *testing.T) {
	migration := testMigration(withPreConversionHook("https://hooks.example/pre-conversion"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	caller := &fakeHookCaller{proceed: true}
	r.HookCaller = caller

	reconcileOnce(t, r, migration)
	updated := readMigration(t, c, migration)
	reconcileOnce(t, r, updated)

	if len(caller.calls) != 1 {
		t.Errorf("hook calls = %d, want 1 (not re-invoked once approved for this generation)", len(caller.calls))
	}
}

func TestReconcileHoldsOnUnapprovedPreRolloutHook(t *testing.T) {
	migration := testMigration(withPreRolloutHook("https://hooks.example/pre-rollout"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.HookCaller = &fakeHookCaller{proceed: false}

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase != phasePending {
		t.Errorf("phase = %q, want %q", got.Status.Phase, phasePending)
	}
	if got.Status.CompletionPercentage != 0 {
		t.Errorf("completionPercentage = %d, want 0 while PreRollout is blocked", got.Status.CompletionPercentage)
	}
	// Conversion itself is not blocked by PreRollout - only traffic movement is.
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Converted != 1 {
		t.Errorf("processed = %+v, want conversion to still run", got.Status.ProcessedStatus)
	}
}

func TestReconcileAdvancesAfterPreRolloutApproves(t *testing.T) {
	migration := testMigration(withPreRolloutHook("https://hooks.example/pre-rollout"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.HookCaller = &fakeHookCaller{proceed: true}

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseComplete {
		t.Errorf("phase = %q, want %q (immediate rollout, approved)", got.Status.Phase, phaseComplete)
	}
	if got.Status.CompletionPercentage != 100 {
		t.Errorf("completionPercentage = %d, want 100", got.Status.CompletionPercentage)
	}
}

func TestReconcileWithoutHookCallerFailsOpen(t *testing.T) {
	migration := testMigration(withPreConversionHook("https://hooks.example/pre-conversion"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	// r.HookCaller left nil.

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Converted != 1 {
		t.Errorf("processed = %+v, want conversion to proceed with no HookCaller wired", got.Status.ProcessedStatus)
	}
}

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/health"
)

type fakeMetricsSource struct {
	comparison *health.Comparison
	err        error
	calls      int
}

func (f *fakeMetricsSource) Compare(
	_ context.Context,
	_ *transfergwv1beta1.TransferGW,
	_ health.Target,
) (*health.Comparison, error) {
	f.calls++
	return f.comparison, f.err
}

// canaryMigration is a canary rollout with a 5% error-rate rollback threshold.
func canaryMigration(mutators ...func(*transfergwv1beta1.TransferGW)) *transfergwv1beta1.TransferGW {
	base := func(m *transfergwv1beta1.TransferGW) {
		m.Generation = 1
		m.Spec.Rollout.Mode = "canary"
		m.Spec.Monitoring = &transfergwv1beta1.MonitoringSpec{
			Enabled: true,
			Thresholds: &transfergwv1beta1.ThresholdSpec{
				ErrorRate: ptr.To(0.05),
			},
		}
	}
	return testMigration(append([]func(*transfergwv1beta1.TransferGW){base}, mutators...)...)
}

func healthyMetrics() *health.Comparison {
	return &health.Comparison{ErrorRate: &health.Metric{Ingress: 0.01, Gateway: 0.01}}
}

func breachingMetrics() *health.Comparison {
	return &health.Comparison{ErrorRate: &health.Metric{Ingress: 0.01, Gateway: 0.42}}
}

func readMigration(t *testing.T, c client.Client, m *transfergwv1beta1.TransferGW) *transfergwv1beta1.TransferGW {
	t.Helper()
	got := &transfergwv1beta1.TransferGW{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(m), got); err != nil {
		t.Fatalf("re-reading migration: %v", err)
	}
	return got
}

func TestReconcileCanaryAdvancesWhenHealthy(t *testing.T) {
	migration := canaryMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	source := &fakeMetricsSource{comparison: healthyMetrics()}
	r.MetricsSource = source

	reconcileOnce(t, r, migration)

	if source.calls != 1 {
		t.Errorf("Compare calls = %d, want 1", source.calls)
	}

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseCanary {
		t.Errorf("phase = %q, want %q", got.Status.Phase, phaseCanary)
	}
	if got.Status.CompletionPercentage != 25 {
		t.Errorf("completionPercentage = %d, want the 25%% canary share", got.Status.CompletionPercentage)
	}
	if got.Status.MetricsStatus == nil || got.Status.MetricsStatus.ErrorRate == nil {
		t.Fatalf("expected the comparison to be recorded on status, got %+v", got.Status.MetricsStatus)
	}
	if got.Status.MetricsStatus.ErrorRate.Status != "healthy" {
		t.Errorf("errorRate status = %q, want healthy", got.Status.MetricsStatus.ErrorRate.Status)
	}
	if got.Status.LastHealthCheck == nil || got.Status.NextHealthCheck == nil {
		t.Error("expected the health check timestamps to be recorded")
	}
}

func TestReconcileRollsBackOnBreachedThreshold(t *testing.T) {
	migration := canaryMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.MetricsSource = &fakeMetricsSource{comparison: breachingMetrics()}

	res := reconcileOnce(t, r, migration)
	if res.RequeueAfter != 0 {
		t.Errorf("requeueAfter = %s, want no requeue while held", res.RequeueAfter)
	}

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseRolledBack {
		t.Fatalf("phase = %q, want %q", got.Status.Phase, phaseRolledBack)
	}
	if got.Status.CompletionPercentage != 0 {
		t.Errorf("completionPercentage = %d, want 0 after a rollback", got.Status.CompletionPercentage)
	}
	if got.Status.TrafficRoutingStatus == nil || got.Status.TrafficRoutingStatus.Ingress != 100 {
		t.Errorf("trafficRouting = %+v, want all traffic back on the Ingress", got.Status.TrafficRoutingStatus)
	}
	if got.Status.RollbackGeneration != migration.Generation {
		t.Errorf("rollbackGeneration = %d, want %d", got.Status.RollbackGeneration, migration.Generation)
	}

	var message string
	for _, cond := range got.Status.Conditions {
		if cond.Type == conditionRolledBack {
			message = cond.Message
		}
	}
	if message == "" {
		t.Fatalf("expected a %s condition, got %+v", conditionRolledBack, got.Status.Conditions)
	}
	for _, want := range []string{"error rate", "0.42", "0.05"} {
		if !strings.Contains(message, want) {
			t.Errorf("condition message %q does not mention %q", message, want)
		}
	}
}

func TestReconcileHoldsAfterRollback(t *testing.T) {
	migration := canaryMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	source := &fakeMetricsSource{comparison: breachingMetrics()}
	r.MetricsSource = source

	reconcileOnce(t, r, migration)
	reconcileOnce(t, r, migration)

	if source.calls != 1 {
		t.Errorf("Compare calls = %d, want 1: a held migration must not re-check", source.calls)
	}

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseRolledBack || got.Status.CompletionPercentage != 0 {
		t.Errorf("phase = %q at %d%%, want a migration still held at 0%%",
			got.Status.Phase, got.Status.CompletionPercentage)
	}
}

func TestReconcileResumesAfterSpecEdit(t *testing.T) {
	migration := canaryMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	source := &fakeMetricsSource{comparison: breachingMetrics()}
	r.MetricsSource = source

	reconcileOnce(t, r, migration)

	live := readMigration(t, c, migration)
	live.Spec.Rollout.Canary = &transfergwv1beta1.CanarySpec{InitialPercentage: 10}
	// The fake client does not bump metadata.generation the way the apiserver
	// does on a spec write, so the edit is simulated here.
	live.Generation++
	if err := c.Update(context.Background(), live); err != nil {
		t.Fatalf("updating migration: %v", err)
	}

	source.comparison = healthyMetrics()
	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseCanary {
		t.Fatalf("phase = %q, want the rollout to resume as %q", got.Status.Phase, phaseCanary)
	}
	if got.Status.CompletionPercentage != 10 {
		t.Errorf("completionPercentage = %d, want the edited 10%% initial share", got.Status.CompletionPercentage)
	}
	if got.Status.RollbackGeneration != 0 {
		t.Errorf("rollbackGeneration = %d, want it cleared on resume", got.Status.RollbackGeneration)
	}
}

func TestReconcileSkipsHealthWhenMonitoringDisabled(t *testing.T) {
	migration := canaryMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Spec.Monitoring.Enabled = false
	})
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	source := &fakeMetricsSource{comparison: breachingMetrics()}
	r.MetricsSource = source

	reconcileOnce(t, r, migration)

	if source.calls != 0 {
		t.Errorf("Compare calls = %d, want 0 while monitoring is disabled", source.calls)
	}
	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseCanary || got.Status.CompletionPercentage != 25 {
		t.Errorf("phase = %q at %d%%, want the canary to keep advancing",
			got.Status.Phase, got.Status.CompletionPercentage)
	}
}

func TestReconcileWithoutMetricsSourceKeepsAdvancing(t *testing.T) {
	migration := canaryMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseCanary || got.Status.CompletionPercentage != 25 {
		t.Errorf("phase = %q at %d%%, want the canary unaffected with no metrics source",
			got.Status.Phase, got.Status.CompletionPercentage)
	}
	if got.Status.MetricsStatus != nil {
		t.Errorf("metrics = %+v, want none collected", got.Status.MetricsStatus)
	}
}

func TestReconcileMetricsSourceErrorDoesNotRollBack(t *testing.T) {
	migration := canaryMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.MetricsSource = &fakeMetricsSource{err: errors.New("prometheus is down")}

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseCanary || got.Status.CompletionPercentage != 25 {
		t.Errorf("phase = %q at %d%%, want an unreachable backend to leave the rollout alone",
			got.Status.Phase, got.Status.CompletionPercentage)
	}
}

func TestReconcileSkipsHealthCheckBeforeInterval(t *testing.T) {
	migration := canaryMigration()
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	source := &fakeMetricsSource{comparison: healthyMetrics()}
	r.MetricsSource = source

	reconcileOnce(t, r, migration)
	reconcileOnce(t, r, migration)

	if source.calls != 1 {
		t.Errorf("Compare calls = %d, want 1: the check interval has not elapsed", source.calls)
	}
	if readMigration(t, c, migration).Status.Phase != phaseCanary {
		t.Error("expected the canary to keep running between health checks")
	}
}

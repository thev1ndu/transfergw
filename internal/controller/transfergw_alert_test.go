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
	"errors"
	"testing"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/alert"
)

type fakeNotifier struct {
	calls []alert.Event
	url   string
	err   error
}

func (f *fakeNotifier) Notify(_ context.Context, webhookURL string, event alert.Event) error {
	f.url = webhookURL
	f.calls = append(f.calls, event)
	return f.err
}

func withAlerting(webhookURL string) func(*transfergwv1beta1.TransferGW) {
	return func(m *transfergwv1beta1.TransferGW) {
		if m.Spec.Monitoring == nil {
			m.Spec.Monitoring = &transfergwv1beta1.MonitoringSpec{Enabled: true}
		}
		m.Spec.Monitoring.Alerting = &transfergwv1beta1.AlertingSpec{
			Enabled:    true,
			WebhookUrl: webhookURL,
		}
	}
}

func TestReconcileNotifiesWebhookOnRollback(t *testing.T) {
	migration := canaryMigration(withAlerting("https://hooks.example/rollback"))
	r, _ := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.MetricsSource = &fakeMetricsSource{comparison: breachingMetrics()}
	notifier := &fakeNotifier{}
	r.Alerter = notifier

	reconcileOnce(t, r, migration)

	if len(notifier.calls) != 1 {
		t.Fatalf("Notify calls = %d, want 1: %+v", len(notifier.calls), notifier.calls)
	}
	if notifier.url != "https://hooks.example/rollback" {
		t.Errorf("webhook URL = %q, want the configured one", notifier.url)
	}
	event := notifier.calls[0]
	if event.Migration != "transfergw/demo-migration" {
		t.Errorf("event.Migration = %q, want namespace/name", event.Migration)
	}
	if event.Phase != phaseRolledBack {
		t.Errorf("event.Phase = %q, want %q", event.Phase, phaseRolledBack)
	}
	if event.Message == "" {
		t.Error("event.Message is empty, want the breach description")
	}
}

func TestReconcileSkipsNotifyWhenAlertingDisabled(t *testing.T) {
	migration := canaryMigration() // no Alerting set
	r, _ := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.MetricsSource = &fakeMetricsSource{comparison: breachingMetrics()}
	notifier := &fakeNotifier{}
	r.Alerter = notifier

	reconcileOnce(t, r, migration)

	if len(notifier.calls) != 0 {
		t.Errorf("Notify calls = %d, want 0 when alerting is not configured", len(notifier.calls))
	}
}

func TestReconcileSkipsNotifyWithoutAlerter(t *testing.T) {
	migration := canaryMigration(withAlerting("https://hooks.example/rollback"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.MetricsSource = &fakeMetricsSource{comparison: breachingMetrics()}
	// r.Alerter left nil.

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseRolledBack {
		t.Fatalf("phase = %q, want %q even without an Alerter", got.Status.Phase, phaseRolledBack)
	}
}

func TestReconcileLogsButIgnoresNotifyError(t *testing.T) {
	migration := canaryMigration(withAlerting("https://hooks.example/rollback"))
	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"}),
		migration,
	)
	r.MetricsSource = &fakeMetricsSource{comparison: breachingMetrics()}
	r.Alerter = &fakeNotifier{err: errors.New("webhook unreachable")}

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.Phase != phaseRolledBack {
		t.Errorf("phase = %q, want %q despite the failed webhook", got.Status.Phase, phaseRolledBack)
	}
}

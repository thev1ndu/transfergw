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

// Package alert notifies external systems about migration events, currently
// just a webhook POST on health rollback.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Event describes a migration event worth notifying about.
type Event struct {
	Migration string `json:"migration"`
	Phase     string `json:"phase"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Time      string `json:"time"`
}

// Notifier delivers an Event to whatever spec.monitoring.alerting names.
//
// An interface so the controller can be tested with a fake instead of
// exercising real HTTP calls; WebhookNotifier is the only implementation for
// now since that's the only alerting channel actually wired end to end.
type Notifier interface {
	Notify(ctx context.Context, webhookURL string, event Event) error
}

// WebhookNotifier POSTs the event as JSON to an arbitrary URL. It has no
// constructor-time configuration because the destination varies per
// migration (spec.monitoring.alerting.webhookUrl), not per cluster.
type WebhookNotifier struct {
	Client *http.Client
}

// NewWebhookNotifier returns a WebhookNotifier with a bounded request
// timeout, so a slow or unreachable webhook can never hold up a reconcile.
func NewWebhookNotifier() *WebhookNotifier {
	return &WebhookNotifier{Client: &http.Client{Timeout: 5 * time.Second}}
}

func (w *WebhookNotifier) Notify(ctx context.Context, webhookURL string, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encoding alert payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building alert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.Client.Do(req)
	if err != nil {
		return fmt.Errorf("sending alert webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("alert webhook returned %s", resp.Status)
	}
	return nil
}

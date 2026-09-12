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

// Package lifecycle calls spec.lifecycle.hooks webhooks at fixed points in a
// migration and reports whether the migration may proceed past that point.
package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Stage identifies which point in a migration's lifecycle a hook fires at.
type Stage string

const (
	// PreConversion gates whether the controller converts any Ingress at all.
	PreConversion Stage = "PreConversion"

	// PostConversion notifies once HTTPRoutes have been generated. It never
	// blocks: undoing a conversion that already happened isn't meaningful.
	PostConversion Stage = "PostConversion"

	// PreRollout gates whether traffic may start moving onto the Gateway path
	// at all. Once it approves, the canary/gradual schedule in
	// spec.rollout runs unattended; PreRollout is not re-checked per step.
	PreRollout Stage = "PreRollout"

	// PostRollout notifies once the migration reaches 100%. It never blocks:
	// there is nothing left to hold back once traffic has fully moved.
	PostRollout Stage = "PostRollout"
)

// Event describes the migration and stage a hook fires for.
type Event struct {
	Migration string `json:"migration"`
	Stage     string `json:"stage"`
	Time      string `json:"time"`
}

// Caller invokes a lifecycle webhook and reports whether the migration may
// proceed past that stage.
type Caller interface {
	Call(ctx context.Context, webhookURL string, event Event) (proceed bool, err error)
}

// WebhookCaller calls a plain HTTP webhook. A 2xx response means proceed;
// anything else, including a network error, means hold.
//
// The timeout is short deliberately: a hook that gates on human approval
// (e.g. an on-call ack) must respond immediately with "not yet" and let
// TransferGW poll again next reconcile, not hold the HTTP request open while
// waiting for a person. A hook that blocks for the approval itself would
// stall the reconciler's worker.
type WebhookCaller struct {
	Client *http.Client
}

// NewWebhookCaller returns a WebhookCaller with a bounded request timeout.
func NewWebhookCaller() *WebhookCaller {
	return &WebhookCaller{Client: &http.Client{Timeout: 10 * time.Second}}
}

func (w *WebhookCaller) Call(ctx context.Context, webhookURL string, event Event) (bool, error) {
	body, err := json.Marshal(event)
	if err != nil {
		return false, fmt.Errorf("encoding lifecycle hook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("building lifecycle hook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("calling lifecycle hook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("lifecycle hook returned %s", resp.Status)
	}
	return true, nil
}

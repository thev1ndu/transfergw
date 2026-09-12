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

package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookNotifierPostsJSONEvent(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotBody        Event
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	n := NewWebhookNotifier()
	event := Event{
		Migration: "demo/rollout",
		Phase:     "RolledBack",
		Reason:    "ThresholdBreached",
		Message:   "error rate 0.42 exceeds 0.05",
		Time:      "2026-01-01T00:00:00Z",
	}
	if err := n.Notify(context.Background(), server.URL, event); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if gotBody != event {
		t.Errorf("received event = %+v, want %+v", gotBody, event)
	}
}

func TestWebhookNotifierErrorsOnNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	n := NewWebhookNotifier()
	if err := n.Notify(context.Background(), server.URL, Event{}); err == nil {
		t.Error("expected an error for a 500 response, got nil")
	}
}

func TestWebhookNotifierErrorsOnUnreachableURL(t *testing.T) {
	n := NewWebhookNotifier()
	if err := n.Notify(context.Background(), "http://127.0.0.1:0", Event{}); err == nil {
		t.Error("expected an error for an unreachable URL, got nil")
	}
}

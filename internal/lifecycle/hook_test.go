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

package lifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookCallerProceedsOn2xx(t *testing.T) {
	var gotBody Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	event := Event{Migration: "demo/rollout", Stage: string(PreConversion), Time: "2026-01-01T00:00:00Z"}
	proceed, err := NewWebhookCaller().Call(context.Background(), server.URL, event)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !proceed {
		t.Error("proceed = false, want true for a 200 response")
	}
	if gotBody != event {
		t.Errorf("received event = %+v, want %+v", gotBody, event)
	}
}

func TestWebhookCallerHoldsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	proceed, err := NewWebhookCaller().Call(context.Background(), server.URL, Event{})
	if err == nil {
		t.Error("expected an error for a 403 response, got nil")
	}
	if proceed {
		t.Error("proceed = true, want false for a 403 response")
	}
}

func TestWebhookCallerHoldsOnUnreachableURL(t *testing.T) {
	proceed, err := NewWebhookCaller().Call(context.Background(), "http://127.0.0.1:0", Event{})
	if err == nil {
		t.Error("expected an error for an unreachable URL, got nil")
	}
	if proceed {
		t.Error("proceed = true, want false when the hook can't be reached")
	}
}

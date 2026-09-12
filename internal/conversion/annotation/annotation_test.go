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

package annotation

import (
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestCookieSessionPersistenceTranslatorEnablesOnCookieOrTrue(t *testing.T) {
	tr := &CookieSessionPersistenceTranslator{}
	for _, value := range []string{"true", "cookie"} {
		effect, issue := tr.Effect("affinity", value)
		if issue != nil {
			t.Fatalf("value %q: issue = %+v, want none", value, issue)
		}
		if effect == nil || effect.SessionPersistence == nil {
			t.Fatalf("value %q: effect = %+v, want a SessionPersistence", value, effect)
		}
		if got := *effect.SessionPersistence.Type; got != gatewayv1.CookieBasedSessionPersistence {
			t.Errorf("value %q: type = %q, want Cookie", value, got)
		}
	}
}

func TestCookieSessionPersistenceTranslatorNoOpOnOtherValues(t *testing.T) {
	tr := &CookieSessionPersistenceTranslator{}
	effect, issue := tr.Effect("affinity", "false")
	if issue != nil || effect != nil {
		t.Errorf("effect = %+v, issue = %+v, want none for a disabling value", effect, issue)
	}
}

func TestBackendRequestTimeoutTranslatorParsesSeconds(t *testing.T) {
	tr := &BackendRequestTimeoutTranslator{}
	effect, issue := tr.Effect("proxy-read-timeout", "60")
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if effect == nil || effect.Timeouts == nil || effect.Timeouts.BackendRequest == nil {
		t.Fatalf("effect = %+v, want a BackendRequest timeout", effect)
	}
	if got := string(*effect.Timeouts.BackendRequest); got != "60s" {
		t.Errorf("backendRequest = %q, want 60s", got)
	}
}

func TestRequestTimeoutTranslatorParsesSeconds(t *testing.T) {
	tr := &RequestTimeoutTranslator{}
	effect, issue := tr.Effect("request-timeout", "30")
	if issue != nil {
		t.Fatalf("issue = %+v, want none", issue)
	}
	if effect == nil || effect.Timeouts == nil || effect.Timeouts.Request == nil {
		t.Fatalf("effect = %+v, want a Request timeout", effect)
	}
	if got := string(*effect.Timeouts.Request); got != "30s" {
		t.Errorf("request = %q, want 30s", got)
	}
}

func TestTimeoutTranslatorsWarnOnNonNumericValue(t *testing.T) {
	tr := &RequestTimeoutTranslator{}
	effect, issue := tr.Effect("request-timeout", "not-a-number")
	if effect != nil {
		t.Errorf("effect = %+v, want none for an invalid value", effect)
	}
	if issue == nil || issue.Severity != SeverityWarning {
		t.Errorf("issue = %+v, want a warning", issue)
	}
}

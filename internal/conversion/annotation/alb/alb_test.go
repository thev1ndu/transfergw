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

package alb

import "testing"

func TestEveryRegisteredAnnotationHasATranslator(t *testing.T) {
	for annotation, translator := range Translators {
		if translator == nil {
			t.Errorf("%s is registered with a nil translator", annotation)
		}
	}
}

func TestUnsupportedAnnotationsWarnWithSpecificGuidance(t *testing.T) {
	for key := range Translators {
		tr := Translators[key]
		_, issue := tr.Translate(key, "x")
		if issue == nil {
			t.Fatalf("%s: expected an issue, got none", key)
		}
		if issue.Recommendation == "" {
			t.Errorf("%s: expected a non-empty recommendation", key)
		}
	}
}

func TestBackendProtocolOnlyFiresForNonGRPCValues(t *testing.T) {
	// backend-protocol: GRPC is intercepted structurally before this
	// translator ever runs - see conversion.isGRPCBackend - so this
	// translator only needs to handle every other value.
	tr := Translators[Prefix+"backend-protocol"]
	_, issue := tr.Translate(Prefix+"backend-protocol", "HTTPS")
	if issue == nil {
		t.Fatalf("expected an issue for a non-GRPC backend-protocol value")
	}
}

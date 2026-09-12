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

package conversion

import (
	"fmt"
	"strings"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// nginxAnnotationPrefix is shared by every ingress-nginx annotation.
const nginxAnnotationPrefix = "nginx.ingress.kubernetes.io/"

// nginxTranslators maps each supported ingress-nginx annotation to the
// Translator that handles it. To add support for another nginx annotation,
// implement Translator and add one entry here.
var nginxTranslators = map[string]Translator{
	nginxAnnotationPrefix + "rewrite-target": &RewriteTranslator{},
	nginxAnnotationPrefix + "limit-rps":      &RateLimitTranslator{},
	nginxAnnotationPrefix + "auth-type":      &AuthTranslator{},
	nginxAnnotationPrefix + "auth-url":       &AuthTranslator{},
}

// RewriteTranslator maps nginx's rewrite-target onto a URLRewrite filter.
type RewriteTranslator struct{}

func (t *RewriteTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	if value == "" {
		return nil, nil
	}
	if strings.ContainsAny(value, "$()") {
		return nil, &Issue{
			Severity: SeverityWarning,
			Message: fmt.Sprintf("rewrite-target %q uses capture groups, which URLRewrite "+
				"cannot express", value),
			Recommendation: "Replace the capture-group rewrite with a static prefix rewrite.",
		}
	}
	return []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterURLRewrite,
		URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
			Path: &gatewayv1.HTTPPathModifier{
				Type:               gatewayv1.PrefixMatchHTTPPathModifier,
				ReplacePrefixMatch: ptr.To(value),
			},
		},
	}}, nil
}

// RateLimitTranslator reports that rate limiting needs an out-of-band policy.
type RateLimitTranslator struct{}

func (t *RateLimitTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: "Express rate limiting with your implementation's policy CRD.",
	}
}

// AuthTranslator reports that external auth needs an out-of-band policy.
type AuthTranslator struct{}

func (t *AuthTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityWarning,
		Message:        fmt.Sprintf("%s=%s has no core Gateway API equivalent", key, value),
		Recommendation: "Express authentication with your implementation's policy CRD.",
	}
}

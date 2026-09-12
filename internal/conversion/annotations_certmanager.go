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

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// certManagerAnnotationPrefix is shared by every cert-manager annotation.
const certManagerAnnotationPrefix = "cert-manager.io/"

// certManagerTranslators maps each supported cert-manager annotation to the
// Translator that handles it. To add support for another cert-manager
// annotation, implement Translator and add one entry here.
var certManagerTranslators = map[string]Translator{
	certManagerAnnotationPrefix + "cluster-issuer": &CertManagerTranslator{},
	certManagerAnnotationPrefix + "issuer":         &CertManagerTranslator{},
}

// CertManagerTranslator reports that cert-manager wiring moves to the Gateway.
type CertManagerTranslator struct{}

func (t *CertManagerTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *Issue) {
	return nil, &Issue{
		Severity:       SeverityInfo,
		Message:        fmt.Sprintf("%s=%s applies to the Gateway listener, not the HTTPRoute", key, value),
		Recommendation: "Move this annotation onto the Gateway resource.",
	}
}

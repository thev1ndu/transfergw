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

package nginx

import (
	"strconv"
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/thev1ndu/transfergw/internal/conversion/annotation"
)

// CORSTranslator maps enable-cors, plus whichever cors-* siblings are also
// set, onto a single core Gateway API CORS filter. It needs
// annotation.ContextualTranslator: CORS is inherently several annotations
// combined into one filter, which a plain Translator (one key/value at a
// time) can't see.
type CORSTranslator struct{}

func (t *CORSTranslator) Translate(key, value string) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	return nil, nil
}

func (t *CORSTranslator) TranslateWithContext(
	key, value string,
	all map[string]string,
) ([]gatewayv1.HTTPRouteFilter, *annotation.Issue) {
	if value != "true" {
		return nil, nil
	}

	cors := &gatewayv1.HTTPCORSFilter{}

	if v := all[Prefix+"cors-allow-origin"]; v != "" {
		for _, o := range strings.Split(v, ",") {
			cors.AllowOrigins = append(cors.AllowOrigins, gatewayv1.CORSOrigin(strings.TrimSpace(o)))
		}
	} else {
		// nginx defaults to allowing every origin when enable-cors is set
		// with no explicit allow-list.
		cors.AllowOrigins = []gatewayv1.CORSOrigin{"*"}
	}

	if v := all[Prefix+"cors-allow-methods"]; v != "" {
		for _, m := range strings.Split(v, ",") {
			cors.AllowMethods = append(cors.AllowMethods, gatewayv1.HTTPMethodWithWildcard(strings.TrimSpace(m)))
		}
	}

	if v := all[Prefix+"cors-allow-headers"]; v != "" {
		for _, h := range strings.Split(v, ",") {
			cors.AllowHeaders = append(cors.AllowHeaders, gatewayv1.HTTPHeaderName(strings.TrimSpace(h)))
		}
	}

	if v := all[Prefix+"cors-expose-headers"]; v != "" {
		for _, h := range strings.Split(v, ",") {
			cors.ExposeHeaders = append(cors.ExposeHeaders, gatewayv1.HTTPHeaderName(strings.TrimSpace(h)))
		}
	}

	// nginx defaults cors-allow-credentials to true.
	allowCredentials := true
	if v, ok := all[Prefix+"cors-allow-credentials"]; ok {
		allowCredentials = v == "true"
	}
	cors.AllowCredentials = &allowCredentials

	if v := all[Prefix+"cors-max-age"]; v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cors.MaxAge = int32(n)
		}
	}

	return []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterCORS,
		CORS: cors,
	}}, nil
}

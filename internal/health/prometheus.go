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

package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
)

// Queries holds one PromQL template per measurement and path.
//
// There is no metric naming that is common to every Ingress controller and
// every Gateway implementation, so the queries are templates the operator is
// expected to override rather than something this project can get right for
// everyone. The defaults below only fit ingress-nginx and Envoy Gateway; an
// empty template means the metric is not collected and therefore never causes
// a rollback.
//
// Templates are rendered with: .Gateway, .GatewayNamespace, .NamespaceRegex,
// .Window and .Quantile.
type Queries struct {
	IngressErrorRate string
	GatewayErrorRate string

	IngressLatencyMs string
	GatewayLatencyMs string

	IngressConnectionResets string
	GatewayConnectionResets string

	IngressThroughput string
	GatewayThroughput string
}

// DefaultQueries returns queries for ingress-nginx on the Ingress path and
// Envoy Gateway on the Gateway path.
//
// Connection resets have no default: neither project exposes a reset counter
// that means the same thing on both sides, and guessing one would silently
// compare unrelated numbers.
func DefaultQueries() Queries {
	return Queries{
		IngressErrorRate: `sum(rate(nginx_ingress_controller_requests{namespace=~"{{.NamespaceRegex}}",status=~"5.."}[{{.Window}}])) ` +
			`/ clamp_min(sum(rate(nginx_ingress_controller_requests{namespace=~"{{.NamespaceRegex}}"}[{{.Window}}])), 1e-9)`,
		GatewayErrorRate: `sum(rate(envoy_http_downstream_rq_xx{gateway_name="{{.Gateway}}",envoy_response_code_class="5"}[{{.Window}}])) ` +
			`/ clamp_min(sum(rate(envoy_http_downstream_rq_xx{gateway_name="{{.Gateway}}"}[{{.Window}}])), 1e-9)`,

		// nginx reports seconds, Envoy reports milliseconds.
		IngressLatencyMs: `histogram_quantile({{.Quantile}}, sum by (le) ` +
			`(rate(nginx_ingress_controller_request_duration_seconds_bucket{namespace=~"{{.NamespaceRegex}}"}[{{.Window}}]))) * 1000`,
		GatewayLatencyMs: `histogram_quantile({{.Quantile}}, sum by (le) ` +
			`(rate(envoy_http_downstream_rq_time_bucket{gateway_name="{{.Gateway}}"}[{{.Window}}])))`,

		IngressThroughput: `sum(rate(nginx_ingress_controller_requests{namespace=~"{{.NamespaceRegex}}"}[{{.Window}}]))`,
		GatewayThroughput: `sum(rate(envoy_http_downstream_rq_xx{gateway_name="{{.Gateway}}"}[{{.Window}}]))`,
	}
}

// PrometheusSource is a MetricsSource backed by a Prometheus query API.
type PrometheusSource struct {
	URL     string
	Queries Queries
	Client  *http.Client
}

// NewPrometheusSource builds a source pointed at a Prometheus query endpoint,
// e.g. http://prometheus.monitoring.svc:9090.
func NewPrometheusSource(endpoint string) *PrometheusSource {
	return &PrometheusSource{
		URL:     strings.TrimSuffix(endpoint, "/"),
		Queries: DefaultQueries(),
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

type queryVars struct {
	Gateway          string
	GatewayNamespace string
	NamespaceRegex   string
	Window           string
	Quantile         string
}

// Compare runs each configured query pair and returns the measurements that
// came back with data.
func (p *PrometheusSource) Compare(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
	target Target,
) (*Comparison, error) {
	vars := queryVars{
		Gateway:          target.GatewayName,
		GatewayNamespace: target.GatewayNamespace,
		NamespaceRegex:   namespaceRegex(target.Namespaces),
		Window:           comparisonWindow(migration),
		Quantile:         quantile(migration),
	}

	out := &Comparison{}
	pairs := []struct {
		ingress, gateway string
		dst              **Metric
	}{
		{p.Queries.IngressErrorRate, p.Queries.GatewayErrorRate, &out.ErrorRate},
		{p.Queries.IngressLatencyMs, p.Queries.GatewayLatencyMs, &out.LatencyMs},
		{p.Queries.IngressConnectionResets, p.Queries.GatewayConnectionResets, &out.ConnectionResets},
		{p.Queries.IngressThroughput, p.Queries.GatewayThroughput, &out.Throughput},
	}

	for _, pair := range pairs {
		if pair.ingress == "" || pair.gateway == "" {
			continue
		}
		ingress, ok, err := p.scalar(ctx, pair.ingress, vars)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		gateway, ok, err := p.scalar(ctx, pair.gateway, vars)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		*pair.dst = &Metric{Ingress: ingress, Gateway: gateway}
	}
	return out, nil
}

// scalar renders a query template, runs it, and reads the first sample. The
// bool reports whether Prometheus returned any sample at all.
func (p *PrometheusSource) scalar(ctx context.Context, tmpl string, vars queryVars) (float64, bool, error) {
	t, err := template.New("q").Parse(tmpl)
	if err != nil {
		return 0, false, fmt.Errorf("parsing query template: %w", err)
	}
	var rendered strings.Builder
	if err := t.Execute(&rendered, vars); err != nil {
		return 0, false, fmt.Errorf("rendering query template: %w", err)
	}

	endpoint := p.URL + "/api/v1/query?" + url.Values{"query": {rendered.String()}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false, err
	}

	httpClient := p.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("querying prometheus: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("prometheus returned %s for %q", resp.Status, rendered.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, false, fmt.Errorf("decoding prometheus response: %w", err)
	}
	if body.Status != "success" {
		return 0, false, fmt.Errorf("prometheus query %q failed with status %q", rendered.String(), body.Status)
	}
	if len(body.Data.Result) == 0 || len(body.Data.Result[0].Value) != 2 {
		return 0, false, nil
	}

	raw, ok := body.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false, nil
	}
	return v, true, nil
}

var unsafeLabelChars = regexp.MustCompile(`[^A-Za-z0-9_.\-]`)

func namespaceRegex(namespaces []string) string {
	if len(namespaces) == 0 {
		return ".*"
	}
	out := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		out = append(out, unsafeLabelChars.ReplaceAllString(ns, ""))
	}
	return strings.Join(out, "|")
}

func comparisonWindow(migration *transfergwv1beta1.TransferGW) string {
	if m := migration.Spec.Monitoring; m != nil && m.ComparisonWindow != "" {
		if _, err := time.ParseDuration(m.ComparisonWindow); err == nil {
			return m.ComparisonWindow
		}
	}
	return "5m"
}

func quantile(migration *transfergwv1beta1.TransferGW) string {
	percentile := ""
	if m := migration.Spec.Monitoring; m != nil && m.Thresholds != nil {
		percentile = m.Thresholds.LatencyPercentile
	}
	switch percentile {
	case "p50":
		return "0.50"
	case "p75":
		return "0.75"
	case "p90":
		return "0.90"
	case "p99":
		return "0.99"
	default:
		return "0.95"
	}
}

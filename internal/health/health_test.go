package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
)

func thresholds() *transfergwv1beta1.ThresholdSpec {
	return &transfergwv1beta1.ThresholdSpec{
		ErrorRate:         ptr.To(0.05),
		LatencyMs:         150,
		LatencyPercentile: "p95",
		ConnectionResets:  0.01,
	}
}

func TestEvaluateHealthyComparison(t *testing.T) {
	c := &Comparison{
		ErrorRate:        &Metric{Ingress: 0.01, Gateway: 0.02},
		LatencyMs:        &Metric{Ingress: 90, Gateway: 120},
		ConnectionResets: &Metric{Ingress: 0.001, Gateway: 0.002},
	}
	if b := Evaluate(thresholds(), c); b != nil {
		t.Errorf("Evaluate = %v, want no breach below every threshold", b)
	}
}

func TestEvaluateErrorRateBreach(t *testing.T) {
	c := &Comparison{ErrorRate: &Metric{Ingress: 0.01, Gateway: 0.4}}

	b := Evaluate(thresholds(), c)
	if b == nil {
		t.Fatal("expected an error rate breach")
	}
	if b.Metric != "error rate" || b.Gateway != 0.4 || b.Threshold != 0.05 {
		t.Errorf("breach = %+v, want the error rate against its 0.05 threshold", b)
	}
	if !strings.Contains(b.String(), "0.4") {
		t.Errorf("message %q should carry the measured value", b.String())
	}
}

func TestEvaluateIgnoresBreachThatAlsoAffectsIngress(t *testing.T) {
	c := &Comparison{ErrorRate: &Metric{Ingress: 0.5, Gateway: 0.4}}
	if b := Evaluate(thresholds(), c); b != nil {
		t.Errorf("Evaluate = %v, want no breach when the Ingress path is worse", b)
	}
}

func TestEvaluateLatencyBreachNamesThePercentile(t *testing.T) {
	c := &Comparison{LatencyMs: &Metric{Ingress: 100, Gateway: 900}}

	b := Evaluate(thresholds(), c)
	if b == nil {
		t.Fatal("expected a latency breach")
	}
	if !strings.Contains(b.Metric, "p95") {
		t.Errorf("metric = %q, want the configured percentile named", b.Metric)
	}
}

func TestEvaluateSkipsUnsetThresholds(t *testing.T) {
	c := &Comparison{
		ErrorRate:        &Metric{Ingress: 0.01, Gateway: 0.9},
		ConnectionResets: &Metric{Ingress: 0, Gateway: 0.9},
	}
	if b := Evaluate(&transfergwv1beta1.ThresholdSpec{}, c); b != nil {
		t.Errorf("Evaluate = %v, want no breach when nothing is configured", b)
	}
}

func TestEvaluateSkipsMissingMeasurements(t *testing.T) {
	if b := Evaluate(thresholds(), &Comparison{}); b != nil {
		t.Errorf("Evaluate = %v, want no breach without any data", b)
	}
	if b := Evaluate(nil, &Comparison{ErrorRate: &Metric{Gateway: 1}}); b != nil {
		t.Errorf("Evaluate = %v, want no breach without thresholds", b)
	}
}

func TestEvaluateThroughputDropBreach(t *testing.T) {
	spec := &transfergwv1beta1.ThresholdSpec{ThroughputDelta: ptr.To(0.2)}
	c := &Comparison{Throughput: &Metric{Ingress: 100, Gateway: 50}}

	b := Evaluate(spec, c)
	if b == nil {
		t.Fatal("expected a throughput breach")
	}
	if b.Threshold != 80 {
		t.Errorf("threshold = %g, want the 80 req/s floor implied by a 20%% delta", b.Threshold)
	}
	if b2 := Evaluate(spec, &Comparison{Throughput: &Metric{Ingress: 100, Gateway: 90}}); b2 != nil {
		t.Errorf("Evaluate = %v, want no breach for a drop inside the delta", b2)
	}
}

func TestPrometheusSourceComparesBothPaths(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)

		value := "0.01"
		if strings.Contains(q, "envoy_") {
			value = "0.42"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":` +
			`[{"metric":{},"value":[1735689600,"` + value + `"]}]}}`))
	}))
	defer srv.Close()

	source := NewPrometheusSource(srv.URL)
	// Only the error rate is exercised; the rest would just repeat the plumbing.
	source.Queries = Queries{
		IngressErrorRate: `nginx_errors{namespace=~"{{.NamespaceRegex}}"}[{{.Window}}]`,
		GatewayErrorRate: `envoy_errors{gateway_name="{{.Gateway}}"}[{{.Window}}]`,
	}

	migration := &transfergwv1beta1.TransferGW{
		Spec: transfergwv1beta1.TransferGWSpec{
			Monitoring: &transfergwv1beta1.MonitoringSpec{ComparisonWindow: "10m"},
		},
	}
	got, err := source.Compare(context.Background(), migration, Target{
		GatewayName:      "demo-gateway",
		GatewayNamespace: "transfergw",
		Namespaces:       []string{"demo", "shop"},
	})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if got.ErrorRate == nil {
		t.Fatal("expected an error rate measurement")
	}
	if got.ErrorRate.Ingress != 0.01 || got.ErrorRate.Gateway != 0.42 {
		t.Errorf("errorRate = %+v, want ingress 0.01 and gateway 0.42", got.ErrorRate)
	}
	if got.LatencyMs != nil || got.Throughput != nil {
		t.Error("expected unconfigured queries to be skipped")
	}

	if len(queries) != 2 {
		t.Fatalf("queries = %v, want one per path", queries)
	}
	if !strings.Contains(queries[0], `namespace=~"demo|shop"`) || !strings.Contains(queries[0], "[10m]") {
		t.Errorf("ingress query %q did not render the namespaces and window", queries[0])
	}
	if !strings.Contains(queries[1], `gateway_name="demo-gateway"`) {
		t.Errorf("gateway query %q did not render the gateway name", queries[1])
	}
}

func TestPrometheusSourceReportsQueryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	source := NewPrometheusSource(srv.URL)
	_, err := source.Compare(context.Background(), &transfergwv1beta1.TransferGW{}, Target{GatewayName: "g"})
	if err == nil {
		t.Fatal("expected an error from a failing Prometheus")
	}
}

func TestPrometheusSourceTreatsEmptyResultAsNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	source := NewPrometheusSource(srv.URL)
	got, err := source.Compare(context.Background(), &transfergwv1beta1.TransferGW{}, Target{GatewayName: "g"})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if got.ErrorRate != nil || got.LatencyMs != nil || got.Throughput != nil {
		t.Errorf("comparison = %+v, want nothing measured", got)
	}
}

func TestDefaultQueriesRenderValidTemplates(t *testing.T) {
	source := NewPrometheusSource("http://prometheus:9090")
	migration := &transfergwv1beta1.TransferGW{
		Spec: transfergwv1beta1.TransferGWSpec{
			Monitoring: &transfergwv1beta1.MonitoringSpec{
				Thresholds: &transfergwv1beta1.ThresholdSpec{LatencyPercentile: "p99"},
			},
		},
	}

	var rendered []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rendered = append(rendered, r.URL.Query().Get("query"))
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer srv.Close()
	source.URL = srv.URL

	if _, err := source.Compare(context.Background(), migration, Target{
		GatewayName: "demo-gateway", Namespaces: []string{"demo"},
	}); err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if len(rendered) == 0 {
		t.Fatal("expected the default queries to be issued")
	}
	for _, q := range rendered {
		if strings.Contains(q, "{{") {
			t.Errorf("query %q was not rendered", q)
		}
	}
	if !strings.Contains(rendered[0], "0.99") && !strings.Contains(strings.Join(rendered, " "), "0.99") {
		t.Error("expected the p99 percentile to reach the latency query")
	}
}

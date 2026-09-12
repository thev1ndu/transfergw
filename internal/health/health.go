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
	"fmt"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
)

// Target identifies the two request paths a comparison runs over: the original
// Ingress path and the generated Gateway path.
type Target struct {
	GatewayName      string
	GatewayNamespace string

	// Namespaces holds the namespaces the selected Ingresses live in, which is
	// the only label a backend-agnostic query can reliably narrow on.
	Namespaces []string
}

// MetricsSource compares the health of the Ingress path against the Gateway
// path for a migration in progress.
//
// It is an interface so the reconciler can be driven by a fake in tests; the
// Prometheus-backed implementation in this package is the only production one.
type MetricsSource interface {
	Compare(ctx context.Context, migration *transfergwv1beta1.TransferGW, target Target) (*Comparison, error)
}

// Comparison holds the metrics that could be collected. A nil field means the
// backend had no data for that metric, which is never treated as a breach.
type Comparison struct {
	ErrorRate        *Metric
	LatencyMs        *Metric
	ConnectionResets *Metric
	Throughput       *Metric
}

// Metric is one measurement taken on both paths.
type Metric struct {
	Ingress float64
	Gateway float64
}

// Breach records the first threshold a comparison violated.
type Breach struct {
	Metric    string
	Ingress   float64
	Gateway   float64
	Threshold float64
}

func (b *Breach) String() string {
	return fmt.Sprintf("%s: Gateway path %g vs threshold %g (Ingress path %g)",
		b.Metric, b.Gateway, b.Threshold, b.Ingress)
}

// Evaluate reports the first breached threshold, or nil when the Gateway path
// is healthy enough to keep the rollout going.
//
// Every check requires the Gateway path to be both over the absolute threshold
// and worse than the Ingress path. A backend that is already unhealthy behind
// the Ingress would keep failing after a rollback, so rolling back on it would
// only trade an outage for a pointless flap.
func Evaluate(thresholds *transfergwv1beta1.ThresholdSpec, c *Comparison) *Breach {
	if thresholds == nil || c == nil {
		return nil
	}

	if t := thresholds.ErrorRate; t != nil && c.ErrorRate != nil {
		if b := worseThan("error rate", c.ErrorRate, *t); b != nil {
			return b
		}
	}
	// The remaining thresholds are non-pointer, so a zero value cannot be told
	// apart from an unset one and is read as "not configured".
	if t := float64(thresholds.LatencyMs); t > 0 && c.LatencyMs != nil {
		name := "latency"
		if thresholds.LatencyPercentile != "" {
			name = thresholds.LatencyPercentile + " latency"
		}
		if b := worseThan(name+" (ms)", c.LatencyMs, t); b != nil {
			return b
		}
	}
	if t := thresholds.ConnectionResets; t > 0 && c.ConnectionResets != nil {
		if b := worseThan("connection reset rate", c.ConnectionResets, t); b != nil {
			return b
		}
	}
	// Throughput is the one threshold that is relative by definition, so it is
	// turned into the absolute floor the Gateway path has to clear.
	if t := thresholds.ThroughputDelta; t != nil && c.Throughput != nil && c.Throughput.Ingress > 0 {
		floor := c.Throughput.Ingress * (1 - *t)
		if c.Throughput.Gateway < floor {
			return &Breach{
				Metric:    "throughput (req/s)",
				Ingress:   c.Throughput.Ingress,
				Gateway:   c.Throughput.Gateway,
				Threshold: floor,
			}
		}
	}
	return nil
}

func worseThan(name string, m *Metric, threshold float64) *Breach {
	if m.Gateway <= threshold || m.Gateway <= m.Ingress {
		return nil
	}
	return &Breach{Metric: name, Ingress: m.Ingress, Gateway: m.Gateway, Threshold: threshold}
}

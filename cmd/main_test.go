package main

import "testing"

// The probe server is opt-in: leaving HealthProbeBindAddress empty starts no
// listener, so AddHealthzCheck and AddReadyzCheck register against a server
// that never binds. The chart probes :8081, so the pod then crash-loops even
// though the manager is running correctly. Nothing in the type system catches
// this, hence the test.
func TestManagerOptionsBindsHealthProbe(t *testing.T) {
	opts := managerOptions(":8080", ":8081", 9443, true)

	if opts.HealthProbeBindAddress != ":8081" {
		t.Fatalf("HealthProbeBindAddress = %q, want :8081 — an empty value disables the probe server",
			opts.HealthProbeBindAddress)
	}
}

func TestManagerOptionsPassesThroughSettings(t *testing.T) {
	opts := managerOptions(":9090", ":9091", 8443, false)

	if opts.Metrics.BindAddress != ":9090" {
		t.Errorf("Metrics.BindAddress = %q, want :9090", opts.Metrics.BindAddress)
	}
	if opts.HealthProbeBindAddress != ":9091" {
		t.Errorf("HealthProbeBindAddress = %q, want :9091", opts.HealthProbeBindAddress)
	}
	if opts.LeaderElection {
		t.Error("LeaderElection = true, want false")
	}
	if opts.Scheme == nil {
		t.Error("Scheme is nil")
	}
	if opts.WebhookServer == nil {
		t.Error("WebhookServer is nil")
	}
}

// The lease name is part of the API group's identity; drifting back to an
// example.com placeholder would silently split leadership across versions.
func TestManagerOptionsLeaderElectionID(t *testing.T) {
	opts := managerOptions(":8080", ":8081", 9443, true)

	if opts.LeaderElectionID != "transfergw.t-1.dev" {
		t.Errorf("LeaderElectionID = %q, want transfergw.t-1.dev", opts.LeaderElectionID)
	}
}

// Every type the reconciler reads or writes must be registered, or the manager
// fails at cache start rather than at compile time.
func TestSchemeRegistersRequiredTypes(t *testing.T) {
	for _, gvk := range []struct{ group, version, kind string }{
		{"transfergw.t-1.dev", "v1beta1", "TransferGW"},
		{"networking.k8s.io", "v1", "Ingress"},
		{"gateway.networking.k8s.io", "v1", "HTTPRoute"},
		{"gateway.networking.k8s.io", "v1", "Gateway"},
		{"", "v1", "Namespace"},
	} {
		found := false
		for known := range scheme.AllKnownTypes() {
			if known.Group == gvk.group && known.Version == gvk.version && known.Kind == gvk.kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scheme is missing %s/%s %s", gvk.group, gvk.version, gvk.kind)
		}
	}
}

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

package main

import (
	"flag"
	"os"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gatewayexamplecomv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/controller"
	"github.com/thev1ndu/transfergw/internal/conversion"
	"github.com/thev1ndu/transfergw/internal/health"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(networkingv1.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
	utilruntime.Must(gatewayv1beta1.Install(scheme))
	utilruntime.Must(gatewayexamplecomv1beta1.AddToScheme(scheme))
}

// managerOptions builds the manager configuration.
//
// Kept separate from main so the wiring is testable: an unset
// HealthProbeBindAddress silently disables the probe server, which the compiler
// cannot catch and which crash-loops the pod under the chart's default probes.
func managerOptions(metricsAddr, probeAddr string, webhookPort int, leaderElection bool) ctrl.Options {
	return ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: webhookPort,
		}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "transfergw.t-1.dev",
	}
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var webhookPort int
	var prometheusURL string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&prometheusURL, "prometheus-url", os.Getenv("TRANSFERGW_PROMETHEUS_URL"),
		"Base URL of a Prometheus query API, e.g. http://prometheus.monitoring.svc:9090. "+
			"Health-based automatic rollback is disabled when this is empty.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port the webhook server binds to.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(),
		managerOptions(metricsAddr, probeAddr, webhookPort, enableLeaderElection))
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	conversionEngine := conversion.NewEngine()

	// Left nil unless an endpoint was given, so clusters without Prometheus
	// keep the pre-rollback behaviour instead of failing every health check.
	var metricsSource health.MetricsSource
	if prometheusURL != "" {
		metricsSource = health.NewPrometheusSource(prometheusURL)
		setupLog.Info("health-based rollback enabled", "prometheusURL", prometheusURL)
	}

	if err = (&controller.TransferGWReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		ConversionEngine: conversionEngine,
		MetricsSource:    metricsSource,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TransferGW")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager", "version", "v1beta1")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

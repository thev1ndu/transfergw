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

// Command preview renders the Gateway and HTTPRoute resources a TransferGW
// manifest would generate, without creating the TransferGW or any generated
// resource on the cluster.
//
// A dry-run apply of a TransferGW can't show this: client-side dry-run never
// contacts the API server, and server-side dry-run validates but never
// persists the object, so it never produces the watch event that triggers
// the controller's Reconcile. The generated Gateway/HTTPRoute only exist
// because Reconcile ran, so no dry-run of the TransferGW itself can surface
// them. This command runs the same selection and conversion logic the
// controller uses, directly, against a live cluster's Ingresses.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/controller"
	"github.com/thev1ndu/transfergw/internal/conversion"
)

func main() {
	var file string
	flag.StringVar(&file, "f", "", "Path to a TransferGW manifest to preview (required).")
	flag.Parse()

	if file == "" {
		fmt.Fprintln(os.Stderr, "usage: preview -f <transfergw.yaml>")
		os.Exit(2)
	}

	if err := run(file); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(file string) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading %s: %w", file, err)
	}

	migration := &transfergwv1beta1.TransferGW{}
	if err := yaml.Unmarshal(raw, migration); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	if migration.Namespace == "" {
		migration.Namespace = "default"
	}
	if migration.Name == "" {
		return fmt.Errorf("%s: metadata.name is required", file)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return err
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		return err
	}

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w (is KUBECONFIG set, or ~/.kube/config present?)", err)
	}
	cli, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("building Kubernetes client: %w", err)
	}

	ctx := context.Background()
	ingresses, err := controller.SelectIngresses(ctx, cli, migration.Spec.Selector)
	if err != nil {
		return fmt.Errorf("selecting ingresses: %w", err)
	}
	if len(ingresses) == 0 {
		fmt.Println("# No Ingress resources matched this selector.")
		return nil
	}
	fmt.Fprintf(os.Stderr, "# %d Ingress(es) matched the selector\n", len(ingresses))

	targetNS := migration.Spec.Conversion.TargetNamespace
	if targetNS == "" {
		targetNS = migration.Namespace
	}
	gatewayName := migration.Spec.Conversion.GatewayName
	if gatewayName == "" {
		gatewayName = migration.Name + "-gateway"
	}

	var policy *conversion.AnnotationPolicy
	if p := migration.Spec.Conversion.AnnotationPolicy; p != nil {
		policy = &conversion.AnnotationPolicy{Drop: p.Drop}
	}

	if migration.Spec.Conversion.GenerateGateway {
		gw := &gatewayv1.Gateway{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Gateway",
				APIVersion: gatewayv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      gatewayName,
				Namespace: targetNS,
			},
		}
		gw.Spec.GatewayClassName = gatewayv1.ObjectName(migration.Spec.Conversion.GatewayClass)
		gw.Spec.Listeners = controller.BuildListeners(ingresses)
		if err := printYAML(gw); err != nil {
			return err
		}
	}

	resolvePort := func(namespace, serviceName, portName string) (int32, error) {
		svc := &corev1.Service{}
		if err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serviceName}, svc); err != nil {
			return 0, err
		}
		for _, p := range svc.Spec.Ports {
			if p.Name == portName {
				return p.Port, nil
			}
		}
		return 0, fmt.Errorf("service %s/%s has no port named %q", namespace, serviceName, portName)
	}

	engine := conversion.NewEngine()
	for i := range ingresses {
		result := engine.ConvertIngress(&ingresses[i], conversion.Options{
			GatewayName:      gatewayName,
			GatewayNamespace: targetNS,
			AnnotationPolicy: policy,
			PortResolver:     resolvePort,
		})

		for _, issue := range result.Issues {
			fmt.Fprintf(os.Stderr, "# [%s] %s: %s\n", issue.Severity, issue.Ingress, issue.Message)
			if issue.Recommendation != "" {
				fmt.Fprintf(os.Stderr, "#   -> %s\n", issue.Recommendation)
			}
		}
		if result.Route == nil {
			continue
		}

		result.Route.TypeMeta = metav1.TypeMeta{
			Kind:       "HTTPRoute",
			APIVersion: gatewayv1.GroupVersion.String(),
		}
		if err := printYAML(result.Route); err != nil {
			return err
		}
	}
	return nil
}

func printYAML(obj any) error {
	b, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshaling preview manifest: %w", err)
	}
	fmt.Println("---")
	fmt.Print(string(b))
	return nil
}

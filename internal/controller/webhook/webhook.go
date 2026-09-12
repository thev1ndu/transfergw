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

// Package webhook validates a TransferGW at admission time for the mistakes
// that would otherwise only surface after the first reconcile.
package webhook

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/controller/selector"
)

// TransferGWValidator rejects a TransferGW at admission time for the
// mistakes that would otherwise only surface after the first reconcile:
// a GatewayClass that doesn't exist, a Gateway name already owned by
// another TransferGW, or a selector matching nothing.
type TransferGWValidator struct {
	Client client.Client
}

var _ admission.Validator[*transfergwv1beta1.TransferGW] = &TransferGWValidator{}

// +kubebuilder:webhook:path=/validate-transfergw-t-1-dev-v1beta1-transfergw,mutating=false,failurePolicy=fail,sideEffects=None,groups=transfergw.t-1.dev,resources=transfergws,verbs=create;update,versions=v1beta1,name=vtransfergw.kb.io,admissionReviewVersions=v1

// SetupWebhookWithManager registers the validating webhook. Only called when
// webhooks are enabled (see cmd/main.go) - a cluster without a valid TLS cert
// for the webhook server would otherwise fail every TransferGW apply.
func (v *TransferGWValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &transfergwv1beta1.TransferGW{}).
		WithValidator(v).
		Complete()
}

func (v *TransferGWValidator) ValidateCreate(
	ctx context.Context,
	tgw *transfergwv1beta1.TransferGW,
) (admission.Warnings, error) {
	return v.validate(ctx, tgw)
}

func (v *TransferGWValidator) ValidateUpdate(
	ctx context.Context,
	_, tgw *transfergwv1beta1.TransferGW,
) (admission.Warnings, error) {
	return v.validate(ctx, tgw)
}

func (v *TransferGWValidator) ValidateDelete(
	_ context.Context,
	_ *transfergwv1beta1.TransferGW,
) (admission.Warnings, error) {
	return nil, nil
}

func (v *TransferGWValidator) validate(
	ctx context.Context,
	tgw *transfergwv1beta1.TransferGW,
) (admission.Warnings, error) {
	if err := v.checkGatewayClassExists(ctx, tgw); err != nil {
		return nil, err
	}
	if err := v.checkGatewayNotAlreadyOwned(ctx, tgw); err != nil {
		return nil, err
	}

	var warnings admission.Warnings
	ingresses, err := selector.SelectIngresses(ctx, v.Client, tgw.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("evaluating spec.selector: %w", err)
	}
	if len(ingresses) == 0 {
		warnings = append(warnings, "spec.selector matches no Ingress resources yet")
	}
	return warnings, nil
}

func (v *TransferGWValidator) checkGatewayClassExists(ctx context.Context, tgw *transfergwv1beta1.TransferGW) error {
	gc := &gatewayv1.GatewayClass{}
	err := v.Client.Get(ctx, client.ObjectKey{Name: tgw.Spec.Conversion.GatewayClass}, gc)
	switch {
	case apierrors.IsNotFound(err):
		return fmt.Errorf("spec.conversion.gatewayClass %q does not exist in this cluster",
			tgw.Spec.Conversion.GatewayClass)
	case err != nil:
		return fmt.Errorf("checking gatewayClass %q: %w", tgw.Spec.Conversion.GatewayClass, err)
	default:
		return nil
	}
}

// checkGatewayNotAlreadyOwned only applies when this TransferGW would create
// its own Gateway (generateGateway: true). Two migrations naming the same
// Gateway while both intend to own it would otherwise fight over it -
// whichever reconciles second keeps overwriting the first's listeners.
// Attaching to an existing, independently managed Gateway
// (generateGateway: false) is exactly what gatewayName is for, so that case
// is never flagged.
func (v *TransferGWValidator) checkGatewayNotAlreadyOwned(ctx context.Context, tgw *transfergwv1beta1.TransferGW) error {
	if !tgw.Spec.Conversion.GenerateGateway {
		return nil
	}
	wantNS, wantName := desiredGateway(tgw)

	var list transfergwv1beta1.TransferGWList
	if err := v.Client.List(ctx, &list); err != nil {
		return fmt.Errorf("listing existing TransferGWs: %w", err)
	}
	for i := range list.Items {
		other := &list.Items[i]
		if other.Namespace == tgw.Namespace && other.Name == tgw.Name {
			continue // this is an update of itself
		}
		if !other.Spec.Conversion.GenerateGateway {
			continue
		}
		otherNS, otherName := desiredGateway(other)
		if otherNS == wantNS && otherName == wantName {
			return fmt.Errorf("gateway %s/%s is already owned by TransferGW %s/%s",
				wantNS, wantName, other.Namespace, other.Name)
		}
	}
	return nil
}

// desiredGateway mirrors the same defaulting the reconciler itself applies
// (targetNS := conversion.TargetNamespace; if unset, the migration's own
// namespace) - Gateways are namespaced, so the name alone isn't enough to
// tell two migrations' Gateways apart.
func desiredGateway(tgw *transfergwv1beta1.TransferGW) (namespace, name string) {
	namespace = tgw.Spec.Conversion.TargetNamespace
	if namespace == "" {
		namespace = tgw.Namespace
	}
	name = tgw.Spec.Conversion.GatewayName
	if name == "" {
		name = tgw.Name + "-gateway"
	}
	return namespace, name
}

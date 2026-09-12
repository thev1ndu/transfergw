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

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
)

func gatewayClass(name string) *gatewayv1.GatewayClass {
	return &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: "example.com/controller"},
	}
}

func TestValidatorRejectsUnknownGatewayClass(t *testing.T) {
	migration := testMigration()
	_, c := newReconciler(t, namespace("demo"), namespace("transfergw"), migration)
	v := &TransferGWValidator{Client: c}

	if _, err := v.ValidateCreate(context.Background(), migration); err == nil {
		t.Error("expected an error for a gatewayClass that doesn't exist")
	}
}

func TestValidatorAcceptsKnownGatewayClass(t *testing.T) {
	migration := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Spec.Conversion.GatewayClass = "eg"
	})
	_, c := newReconciler(t, namespace("demo"), namespace("transfergw"), gatewayClass("eg"), migration)
	v := &TransferGWValidator{Client: c}

	warnings, err := v.ValidateCreate(context.Background(), migration)
	if err != nil {
		t.Fatalf("ValidateCreate: %v", err)
	}
	// testMigration's selector matches nothing in this test - expect the
	// "no Ingress resources" warning, not an error.
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want one about the empty selector", warnings)
	}
}

func TestValidatorRejectsDuplicateGatewayOwnership(t *testing.T) {
	existing := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Name = "existing"
		m.Spec.Conversion.GatewayClass = "eg"
		m.Spec.Conversion.TargetNamespace = "transfergw"
	})
	incoming := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Name = "incoming"
		m.Spec.Conversion.GatewayClass = "eg"
		m.Spec.Conversion.TargetNamespace = "transfergw"
		m.Spec.Conversion.GatewayName = "existing-gateway" // same Gateway as "existing"'s default name
	})

	_, c := newReconciler(t, namespace("demo"), namespace("transfergw"), gatewayClass("eg"), existing)
	v := &TransferGWValidator{Client: c}

	if _, err := v.ValidateCreate(context.Background(), incoming); err == nil {
		t.Error("expected an error for a Gateway already owned by another TransferGW")
	}
}

func TestValidatorAllowsSameGatewayNameInDifferentNamespaces(t *testing.T) {
	existing := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Name = "existing"
		m.Spec.Conversion.GatewayClass = "eg"
		m.Spec.Conversion.TargetNamespace = "transfergw"
	})
	incoming := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Name = "existing" // same name, but...
		m.Namespace = "other"
		m.Spec.Conversion.GatewayClass = "eg"
		m.Spec.Conversion.TargetNamespace = "other" // ...a different target namespace
	})

	_, c := newReconciler(t, namespace("demo"), namespace("transfergw"), namespace("other"), gatewayClass("eg"), existing)
	v := &TransferGWValidator{Client: c}

	if _, err := v.ValidateCreate(context.Background(), incoming); err != nil {
		t.Errorf("expected no conflict across different target namespaces, got: %v", err)
	}
}

func TestValidatorSkipsOwnershipCheckWhenNotGeneratingAGateway(t *testing.T) {
	existing := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Name = "existing"
		m.Spec.Conversion.GatewayClass = "eg"
		m.Spec.Conversion.TargetNamespace = "transfergw"
	})
	incoming := testMigration(func(m *transfergwv1beta1.TransferGW) {
		m.Name = "incoming"
		m.Spec.Conversion.GatewayClass = "eg"
		m.Spec.Conversion.TargetNamespace = "transfergw"
		m.Spec.Conversion.GatewayName = "existing-gateway"
		m.Spec.Conversion.GenerateGateway = false // attaching to it, not creating/owning it
	})

	_, c := newReconciler(t, namespace("demo"), namespace("transfergw"), gatewayClass("eg"), existing)
	v := &TransferGWValidator{Client: c}

	if _, err := v.ValidateCreate(context.Background(), incoming); err != nil {
		t.Errorf("expected no conflict when attaching to an existing Gateway, got: %v", err)
	}
}

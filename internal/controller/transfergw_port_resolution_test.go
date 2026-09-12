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
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"testing"
)

func testService(name, ns string, port int32, portName string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: portName, Port: port}},
		},
	}
}

func TestReconcileResolvesNamedServicePort(t *testing.T) {
	migration := testMigration()
	ing := testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"})
	ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port = networkingv1.ServiceBackendPort{Name: "http"}

	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testService("sample-nginx", "demo", 8080, "http"),
		ing,
		migration,
	)

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Converted != 1 {
		t.Fatalf("processed = %+v, want 1 converted (named port resolved)", got.Status.ProcessedStatus)
	}
	if got.Status.ProcessedStatus.Failed != 0 {
		t.Errorf("failed = %d, want 0", got.Status.ProcessedStatus.Failed)
	}
}

func TestReconcileFailsWhenNamedServicePortDoesNotExist(t *testing.T) {
	migration := testMigration()
	ing := testIngress("sample-nginx", "demo", map[string]string{"migrate": "true"})
	ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port = networkingv1.ServiceBackendPort{Name: "does-not-exist"}

	r, c := newReconciler(t,
		namespace("demo"), namespace("transfergw"),
		testService("sample-nginx", "demo", 8080, "http"),
		ing,
		migration,
	)

	reconcileOnce(t, r, migration)

	got := readMigration(t, c, migration)
	if got.Status.ProcessedStatus == nil || got.Status.ProcessedStatus.Failed != 1 {
		t.Fatalf("processed = %+v, want 1 failed (port name doesn't exist on the Service)", got.Status.ProcessedStatus)
	}
}

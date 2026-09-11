/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gatewayexamplecomv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/conversion"
)

// TransferGWReconciler reconciles a TransferGW object
type TransferGWReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	ConversionEngine *conversion.Engine
}

// +kubebuilder:rbac:groups=transfergw.t-1.dev,resources=transfergws,verbs=create;delete;deletecollection;get;list;patch;update;watch
// +kubebuilder:rbac:groups=transfergw.t-1.dev,resources=transfergws/status,verbs=get;patch;update
// +kubebuilder:rbac:groups=transfergw.t-1.dev,resources=transfergws/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;patch;update
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;patch;update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways;httproutes;grpcroutes;tlsroutes,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status;httproutes/status;grpcroutes/status;tlsroutes/status,verbs=get;patch;update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=backendtlspolicies;authorizationpolicies,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=create;delete;get;list;patch;update;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TransferGWReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	transferGW := &gatewayexamplecomv1beta1.TransferGW{}
	if err := r.Get(ctx, req.NamespacedName, transferGW); err != nil {
		logger.Error(err, "unable to fetch TransferGW")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling TransferGW",
		"name", transferGW.Name,
		"namespace", transferGW.Namespace,
		"phase", transferGW.Status.Phase)

	// TODO: Implement reconciliation logic
	// 1. Analyze ingress resources
	// 2. Convert to gateway API
	// 3. Deploy gateway resources
	// 4. Manage traffic split
	// 5. Monitor health

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TransferGWReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayexamplecomv1beta1.TransferGW{}).
		Complete(r)
}

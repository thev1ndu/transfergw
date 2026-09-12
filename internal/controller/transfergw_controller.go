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
	"fmt"
	"path"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	transfergwv1beta1 "github.com/thev1ndu/transfergw/api/v1beta1"
	"github.com/thev1ndu/transfergw/internal/conversion"
)

const (
	// managedByLabel marks resources this operator generates, so they can be
	// found again for update and garbage collection.
	managedByLabel = "transfergw.t-1.dev/managed-by"

	// sourceIngressLabel records which Ingress a generated HTTPRoute came from.
	sourceIngressLabel = "transfergw.t-1.dev/source-ingress"

	// requeueInterval paces reconciles while a rollout is still progressing.
	requeueInterval = 30 * time.Second
)

// Migration phases.
const (
	phaseAnalyzing  = "Analyzing"
	phaseConverting = "Converting"
	phaseDeploying  = "Deploying"
	phaseCanary     = "Canary"
	phaseComplete   = "Complete"
	phaseFailed     = "Failed"
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
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=create;delete;get;list;patch;update;watch

// Reconcile drives a TransferGW through analysis, conversion and deployment of
// the Gateway API resources that mirror the selected Ingresses.
func (r *TransferGWReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	migration := &transfergwv1beta1.TransferGW{}
	if err := r.Get(ctx, req.NamespacedName, migration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if migration.Spec.Rollout.Paused {
		logger.Info("migration is paused, skipping reconcile")
		return ctrl.Result{}, nil
	}

	status := migration.Status.DeepCopy()
	status.Phase = phaseAnalyzing

	ingresses, err := r.selectIngresses(ctx, migration)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("selecting ingresses: %w", err)
	}
	logger.Info("selected ingresses", "count", len(ingresses))

	targetNS := migration.Spec.Conversion.TargetNamespace
	if targetNS == "" {
		targetNS = migration.Namespace
	}

	gatewayName := migration.Name + "-gateway"
	var gatewaysCreated int32

	if migration.Spec.Conversion.GenerateGateway {
		status.Phase = phaseDeploying
		if err := r.ensureGateway(ctx, migration, gatewayName, targetNS, ingresses); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensuring gateway: %w", err)
		}
		gatewaysCreated = 1
	}

	status.Phase = phaseConverting

	var (
		issues    []transfergwv1beta1.ConversionIssue
		converted int32
		failed    int32
		desired   = map[string]struct{}{}
	)

	for i := range ingresses {
		ing := &ingresses[i]

		result := r.ConversionEngine.ConvertIngress(ing, conversion.Options{
			GatewayName:      gatewayName,
			GatewayNamespace: targetNS,
			AnnotationPolicy: annotationPolicy(migration),
		})
		issues = append(issues, toStatusIssues(result.Issues)...)

		if result.Route == nil || result.Failed() {
			failed++
			continue
		}

		if err := r.applyRoute(ctx, migration, result.Route); err != nil {
			failed++
			issues = append(issues, transfergwv1beta1.ConversionIssue{
				Ingress:  ing.Namespace + "/" + ing.Name,
				Severity: conversion.SeverityError,
				Issue:    fmt.Sprintf("applying HTTPRoute: %v", err),
			})
			continue
		}

		desired[result.Route.Namespace+"/"+result.Route.Name] = struct{}{}
		converted++
	}

	pruned, err := r.pruneOrphanedRoutes(ctx, migration, desired)
	if err != nil {
		logger.Error(err, "pruning orphaned routes")
	} else if pruned > 0 {
		logger.Info("pruned routes whose Ingress no longer matches", "count", pruned)
	}

	total := int32(len(ingresses))
	status.ProcessedStatus = &transfergwv1beta1.ProcessedStatus{
		Total:     total,
		Converted: converted,
		Failed:    failed,
		Pending:   total - converted - failed,
	}
	status.ResourcesStatus = &transfergwv1beta1.ResourcesStatus{
		Gateways:   gatewaysCreated,
		HTTPRoutes: converted,
	}
	if len(issues) > maxIssues {
		issues = issues[:maxIssues]
	}
	status.Issues = issues

	percentage := rolloutPercentage(migration, converted, failed)
	status.CompletionPercentage = percentage
	status.TrafficRoutingStatus = &transfergwv1beta1.TrafficRoutingStatus{
		Gateway: percentage,
		Ingress: 100 - percentage,
	}
	status.RollbackReady = converted > 0

	switch {
	case total == 0:
		status.Phase = phaseAnalyzing
		setCondition(status, "Ready", metav1.ConditionFalse, "NoMatchingIngresses",
			"No Ingress resources matched the selector")
	case failed > 0 && converted == 0:
		status.Phase = phaseFailed
		setCondition(status, "Ready", metav1.ConditionFalse, "ConversionFailed",
			fmt.Sprintf("All %d selected Ingress resources failed to convert", failed))
	case percentage >= 100:
		status.Phase = phaseComplete
		setCondition(status, "Ready", metav1.ConditionTrue, "MigrationComplete",
			fmt.Sprintf("Converted %d of %d Ingress resources", converted, total))
	default:
		status.Phase = phaseCanary
		setCondition(status, "Ready", metav1.ConditionFalse, "RolloutInProgress",
			fmt.Sprintf("Gateway is receiving %d%% of traffic", percentage))
	}

	now := metav1.Now()
	status.LastTransitionTime = &now

	if err := r.patchStatus(ctx, migration, status); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	if status.Phase == phaseComplete || status.Phase == phaseFailed {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

const maxIssues = 100

// selectIngresses returns the Ingresses matching the migration's selector.
func (r *TransferGWReconciler) selectIngresses(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
) ([]networkingv1.Ingress, error) {
	sel := migration.Spec.Selector

	labelSelector := labels.Everything()
	if sel.IngressSelector != nil {
		s, err := metav1.LabelSelectorAsSelector(sel.IngressSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid ingressSelector: %w", err)
		}
		labelSelector = s
	}

	namespaces, err := r.resolveNamespaces(ctx, sel.Namespaces)
	if err != nil {
		return nil, err
	}

	var out []networkingv1.Ingress
	for _, ns := range namespaces {
		list := &networkingv1.IngressList{}
		if err := r.List(ctx, list,
			client.InNamespace(ns),
			client.MatchingLabelsSelector{Selector: labelSelector},
		); err != nil {
			return nil, fmt.Errorf("listing ingresses in %s: %w", ns, err)
		}
		for i := range list.Items {
			if matchesIngressClass(&list.Items[i], sel.IngressClasses) {
				out = append(out, list.Items[i])
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// resolveNamespaces expands the namespace patterns against the cluster. An
// empty pattern list means every namespace.
func (r *TransferGWReconciler) resolveNamespaces(ctx context.Context, patterns []string) ([]string, error) {
	nsList := &corev1.NamespaceList{}
	if err := r.List(ctx, nsList); err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}

	var out []string
	for _, ns := range nsList.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			continue
		}
		if len(patterns) == 0 || matchesAnyPattern(ns.Name, patterns) {
			out = append(out, ns.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// matchesAnyPattern reports whether name matches any glob pattern.
func matchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == name {
			return true
		}
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// matchesIngressClass reports whether the Ingress belongs to one of the
// requested classes. An empty class list matches everything.
func matchesIngressClass(ing *networkingv1.Ingress, classes []string) bool {
	if len(classes) == 0 {
		return true
	}
	name := ""
	if ing.Spec.IngressClassName != nil {
		name = *ing.Spec.IngressClassName
	} else if v, ok := ing.Annotations["kubernetes.io/ingress.class"]; ok {
		name = v
	}
	for _, c := range classes {
		if c == name {
			return true
		}
	}
	return false
}

// ensureGateway creates or updates the Gateway the generated routes attach to.
func (r *TransferGWReconciler) ensureGateway(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
	name, namespace string,
	ingresses []networkingv1.Ingress,
) error {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, gw, func() error {
		if gw.Labels == nil {
			gw.Labels = map[string]string{}
		}
		gw.Labels[managedByLabel] = migration.Name

		gw.Spec.GatewayClassName = gatewayv1.ObjectName(migration.Spec.Conversion.GatewayClass)
		gw.Spec.Listeners = buildListeners(ingresses)

		// An ownerRef only works when the Gateway shares the migration's
		// namespace; Kubernetes does not garbage collect across namespaces.
		if namespace == migration.Namespace {
			return controllerutil.SetControllerReference(migration, gw, r.Scheme)
		}
		return nil
	})
	return err
}

// buildListeners derives the Gateway listeners from the selected Ingresses.
// Routes from every namespace are allowed, since generated HTTPRoutes live
// alongside their source Ingress rather than in the Gateway's namespace.
func buildListeners(ingresses []networkingv1.Ingress) []gatewayv1.Listener {
	listeners := []gatewayv1.Listener{{
		Name:     "http",
		Protocol: gatewayv1.HTTPProtocolType,
		Port:     80,
		AllowedRoutes: &gatewayv1.AllowedRoutes{
			Namespaces: &gatewayv1.RouteNamespaces{
				From: ptr.To(gatewayv1.NamespacesFromAll),
			},
		},
	}}

	// One HTTPS listener per distinct TLS secret, so certificates carry over.
	seen := map[string]struct{}{}
	for i := range ingresses {
		for _, tls := range ingresses[i].Spec.TLS {
			if tls.SecretName == "" {
				continue
			}
			ref := ingresses[i].Namespace + "/" + tls.SecretName
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}

			listeners = append(listeners, gatewayv1.Listener{
				Name:     gatewayv1.SectionName(fmt.Sprintf("https-%d", len(listeners))),
				Protocol: gatewayv1.HTTPSProtocolType,
				Port:     443,
				TLS: &gatewayv1.ListenerTLSConfig{
					Mode: ptr.To(gatewayv1.TLSModeTerminate),
					CertificateRefs: []gatewayv1.SecretObjectReference{{
						Name:      gatewayv1.ObjectName(tls.SecretName),
						Namespace: ptr.To(gatewayv1.Namespace(ingresses[i].Namespace)),
					}},
				},
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{
						From: ptr.To(gatewayv1.NamespacesFromAll),
					},
				},
			})
		}
	}
	return listeners
}

// applyRoute creates or updates a generated HTTPRoute.
func (r *TransferGWReconciler) applyRoute(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
	desired *gatewayv1.HTTPRoute,
) error {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		if route.Labels == nil {
			route.Labels = map[string]string{}
		}
		route.Labels[managedByLabel] = migration.Name
		route.Labels[sourceIngressLabel] = desired.Name
		route.Spec = desired.Spec

		if route.Namespace == migration.Namespace {
			return controllerutil.SetControllerReference(migration, route, r.Scheme)
		}
		return nil
	})
	return err
}

// pruneOrphanedRoutes deletes generated routes whose source Ingress no longer
// matches the selector.
func (r *TransferGWReconciler) pruneOrphanedRoutes(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
	desired map[string]struct{},
) (int, error) {
	list := &gatewayv1.HTTPRouteList{}
	if err := r.List(ctx, list, client.MatchingLabels{managedByLabel: migration.Name}); err != nil {
		return 0, err
	}

	pruned := 0
	for i := range list.Items {
		route := &list.Items[i]
		if _, keep := desired[route.Namespace+"/"+route.Name]; keep {
			continue
		}
		if err := r.Delete(ctx, route); err != nil && !apierrors.IsNotFound(err) {
			return pruned, err
		}
		pruned++
	}
	return pruned, nil
}

// rolloutPercentage reports how much traffic the Gateway should be taking.
//
// This is the intended split recorded on status. Actually steering client
// traffic between the Ingress and the Gateway happens at the DNS or
// load-balancer layer and is outside this controller's scope.
func rolloutPercentage(migration *transfergwv1beta1.TransferGW, converted, failed int32) int32 {
	if converted == 0 {
		return 0
	}
	if failed > 0 {
		// Hold the rollout rather than advancing over a partial conversion.
		return 0
	}

	switch migration.Spec.Rollout.Mode {
	case "immediate":
		return 100
	case "gradual", "canary":
		return canaryPercentage(migration)
	default:
		return 100
	}
}

// canaryPercentage advances the canary based on elapsed time since the
// migration was created.
func canaryPercentage(migration *transfergwv1beta1.TransferGW) int32 {
	const (
		defaultInitial   = int32(25)
		defaultIncrement = int32(25)
		defaultStep      = time.Hour
	)

	initial, increment, step := defaultInitial, defaultIncrement, defaultStep
	if c := migration.Spec.Rollout.Canary; c != nil {
		if c.InitialPercentage > 0 {
			initial = c.InitialPercentage
		}
		if c.Increment > 0 {
			increment = c.Increment
		}
		if c.StepDuration != "" {
			if d, err := time.ParseDuration(c.StepDuration); err == nil && d > 0 {
				step = d
			}
		}
	}

	elapsed := time.Since(migration.CreationTimestamp.Time)
	steps := int32(elapsed / step)

	pct := initial + steps*increment
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}

func annotationPolicy(migration *transfergwv1beta1.TransferGW) *conversion.AnnotationPolicy {
	p := migration.Spec.Conversion.AnnotationPolicy
	if p == nil {
		return nil
	}
	return &conversion.AnnotationPolicy{Drop: p.Drop}
}

func toStatusIssues(in []conversion.Issue) []transfergwv1beta1.ConversionIssue {
	out := make([]transfergwv1beta1.ConversionIssue, 0, len(in))
	for _, i := range in {
		out = append(out, transfergwv1beta1.ConversionIssue{
			Ingress:        i.Ingress,
			Issue:          i.Message,
			Severity:       i.Severity,
			Recommendation: i.Recommendation,
		})
	}
	return out
}

func setCondition(status *transfergwv1beta1.TransferGWStatus, condType string, s metav1.ConditionStatus, reason, msg string) {
	cond := metav1.Condition{
		Type:               condType,
		Status:             s,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type != condType {
			continue
		}
		if status.Conditions[i].Status == s {
			// Preserve the original transition time when nothing changed.
			cond.LastTransitionTime = status.Conditions[i].LastTransitionTime
		}
		status.Conditions[i] = cond
		return
	}
	status.Conditions = append(status.Conditions, cond)
}

// patchStatus writes the computed status back, retrying once on conflict.
func (r *TransferGWReconciler) patchStatus(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
	status *transfergwv1beta1.TransferGWStatus,
) error {
	patch := client.MergeFrom(migration.DeepCopy())
	migration.Status = *status
	if err := r.Status().Patch(ctx, migration, patch); err != nil {
		if !apierrors.IsConflict(err) {
			return err
		}
		fresh := &transfergwv1beta1.TransferGW{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(migration), fresh); err != nil {
			return err
		}
		freshPatch := client.MergeFrom(fresh.DeepCopy())
		fresh.Status = *status
		return r.Status().Patch(ctx, fresh, freshPatch)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
//
// Ingresses are watched as well as TransferGWs, so that creating or editing an
// Ingress re-runs any migration that might select it.
func (r *TransferGWReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&transfergwv1beta1.TransferGW{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Watches(
			&networkingv1.Ingress{},
			handler.EnqueueRequestsFromMapFunc(r.migrationsForIngress),
		).
		Complete(r)
}

// migrationsForIngress maps an Ingress event onto every TransferGW in the
// cluster, letting the next reconcile decide whether it actually matches.
func (r *TransferGWReconciler) migrationsForIngress(ctx context.Context, _ client.Object) []reconcile.Request {
	list := &transfergwv1beta1.TransferGWList{}
	if err := r.List(ctx, list); err != nil {
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&list.Items[i]),
		})
	}
	return reqs
}

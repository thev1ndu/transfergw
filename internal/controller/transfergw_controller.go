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
	"sync"
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
	"github.com/thev1ndu/transfergw/internal/alert"
	"github.com/thev1ndu/transfergw/internal/conversion"
	"github.com/thev1ndu/transfergw/internal/health"
	"github.com/thev1ndu/transfergw/internal/lifecycle"
)

const (
	// managedByLabel marks resources this operator generates, so they can be
	// found again for update and garbage collection.
	managedByLabel = "transfergw.t-1.dev/managed-by"

	// sourceIngressLabel records which Ingress a generated HTTPRoute came from.
	sourceIngressLabel = "transfergw.t-1.dev/source-ingress"

	// requeueInterval paces reconciles while a rollout is still progressing.
	requeueInterval = 30 * time.Second

	// defaultHealthCheckInterval matches the CRD default for
	// spec.monitoring.interval, which is coarser than requeueInterval so that
	// health checks do not run on every reconcile.
	defaultHealthCheckInterval = 5 * time.Minute
)

// Migration phases.
const (
	phaseAnalyzing  = "Analyzing"
	phaseConverting = "Converting"
	phaseDeploying  = "Deploying"
	phaseCanary     = "Canary"
	phaseComplete   = "Complete"
	phaseFailed     = "Failed"

	// phaseRolledBack means a health threshold was breached mid-canary and the
	// Gateway's traffic share was taken back to rollbackPercentage.
	phaseRolledBack = "RolledBack"

	// phasePending means a blocking lifecycle hook (currently only
	// PreConversion) has not yet approved the migration.
	phasePending = "Pending"
)

// rollbackPercentage is where a health rollback parks the Gateway's traffic
// share: all the way back at zero rather than at the canary's initial step.
//
// Dropping to the initial step would leave real users on a path that was just
// measured as unhealthy, and the initial step is itself a percentage the
// migration already passed through, so it carries no evidence of being safe.
// Zero is the only share that is known good, since it is where the migration
// started.
const rollbackPercentage int32 = 0

// TransferGWReconciler reconciles a TransferGW object
type TransferGWReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	ConversionEngine *conversion.Engine

	// MetricsSource is optional. Without one, health comparison is skipped and
	// a migration behaves exactly as it did before rollback support existed.
	MetricsSource health.MetricsSource

	// Alerter delivers a notification when a health rollback happens. Optional:
	// a migration with spec.monitoring.alerting.enabled but a nil Alerter just
	// skips notifying, the same as leaving alerting unconfigured.
	Alerter alert.Notifier

	// DefaultWebhookURL is used for a migration that leaves
	// spec.monitoring.alerting unset entirely. A migration that sets it
	// explicitly (including Enabled: false) always overrides this.
	DefaultWebhookURL string

	// HookCaller invokes spec.lifecycle.hooks webhooks. Optional: a migration
	// with a hook configured but a nil HookCaller fails open (the hook is
	// treated as approved), the same fail-open choice already made for a nil
	// MetricsSource, logged once so a genuinely missing wiring is visible.
	HookCaller lifecycle.Caller

	warnNoMetricsSource sync.Once
	warnNoHookCaller    sync.Once
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
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
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

	// A rolled-back migration holds until a human edits the spec. Re-running
	// the rollout would put the canary straight back on the percentage that
	// just failed its health check, and the next check would roll it back
	// again: a flap that moves user traffic on and off a broken path every few
	// minutes. Bumping the generation is the operator saying they have looked.
	if migration.Status.Phase == phaseRolledBack &&
		migration.Status.RollbackGeneration == migration.Generation {
		logger.Info("held after a health rollback; edit the TransferGW spec to resume",
			"reason", rollbackReason(migration))
		return ctrl.Result{}, nil
	}

	status := migration.Status.DeepCopy()

	if !r.runHook(ctx, migration, status, lifecycle.PreConversion, true) {
		status.Phase = phasePending
		now := metav1.Now()
		status.LastTransitionTime = &now
		if err := r.patchStatus(ctx, migration, status); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	status.Phase = phaseAnalyzing

	ingresses, err := SelectIngresses(ctx, r.Client, migration.Spec.Selector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("selecting ingresses: %w", err)
	}
	logger.Info("selected ingresses", "count", len(ingresses))

	targetNS := migration.Spec.Conversion.TargetNamespace
	if targetNS == "" {
		targetNS = migration.Namespace
	}

	gatewayName := migration.Spec.Conversion.GatewayName
	if gatewayName == "" {
		gatewayName = migration.Name + "-gateway"
	}
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

	canaryNames, canaryForPrimary := pairCanaries(ingresses)
	convertOpts := conversion.Options{
		GatewayName:      gatewayName,
		GatewayNamespace: targetNS,
		AnnotationPolicy: annotationPolicy(migration),
		PortResolver:     r.resolveServicePort(ctx),
	}

	for i := range ingresses {
		ing := &ingresses[i]
		if canaryNames[ing.Namespace+"/"+ing.Name] {
			// Converted as a second weighted backendRef on its primary's
			// route below, not as an independent HTTPRoute of its own.
			continue
		}

		result := r.ConversionEngine.ConvertIngress(ing, convertOpts)
		issues = append(issues, toStatusIssues(result.Issues)...)

		if result.Route == nil || result.Failed() {
			failed++
			continue
		}

		if pairing, ok := canaryForPrimary[ing.Namespace+"/"+ing.Name]; ok {
			canaryResult := r.ConversionEngine.ConvertIngress(pairing.ingress, convertOpts)
			if canaryResult.Route != nil && !canaryResult.Failed() {
				mergeCanaryBackend(result.Route, canaryResult.Route, pairing.weight)
				converted++ // the canary counts as converted too, folded into this route
				// The canary's own canary/canary-weight annotations would
				// otherwise still report "no core equivalent" - true in
				// general, but misleading here since this is exactly the
				// case that got handled. Any other issue on the canary
				// Ingress (e.g. its own backend problems) still surfaces.
				issues = append(issues, toStatusIssues(nonCanaryIssues(canaryResult.Issues))...)
				issues = append(issues, transfergwv1beta1.ConversionIssue{
					Ingress:  ing.Namespace + "/" + ing.Name,
					Severity: conversion.SeverityInfo,
					Issue: fmt.Sprintf("merged canary Ingress %s/%s as a %d%% weighted backend",
						pairing.ingress.Namespace, pairing.ingress.Name, pairing.weight),
				})
			} else {
				issues = append(issues, toStatusIssues(canaryResult.Issues)...)
				issues = append(issues, transfergwv1beta1.ConversionIssue{
					Ingress:  ing.Namespace + "/" + ing.Name,
					Severity: conversion.SeverityWarning,
					Issue: fmt.Sprintf("canary Ingress %s/%s could not be converted, so its weight was not applied",
						pairing.ingress.Namespace, pairing.ingress.Name),
				})
			}
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

	// Non-blocking: HTTPRoutes already exist by this point, so there is
	// nothing left to hold back. This only records that conversion happened.
	r.runHook(ctx, migration, status, lifecycle.PostConversion, false)

	percentage := rolloutPercentage(migration, converted, failed)

	// PreRollout gates the first move away from 0%, once. It is not
	// re-checked on every subsequent step: once approved, the canary/gradual
	// schedule in spec.rollout runs on its own, same as it always did.
	awaitingPreRollout := percentage > 0 && !r.runHook(ctx, migration, status, lifecycle.PreRollout, true)
	if awaitingPreRollout {
		percentage = 0
	}

	// Health only says something useful while traffic is genuinely split: at
	// 0% there is nothing on the Gateway path to measure, and at 100% there is
	// no Ingress path left to compare against.
	var breach *health.Breach
	if percentage > 0 && percentage < 100 {
		if breach = r.evaluateHealth(ctx, migration, health.Target{
			GatewayName:      gatewayName,
			GatewayNamespace: targetNS,
			Namespaces:       ingressNamespaces(ingresses),
		}, status); breach != nil {
			logger.Info("health threshold breached, rolling the migration back",
				"breach", breach.String(), "from", percentage)
			percentage = rollbackPercentage
		}
	}

	status.CompletionPercentage = percentage
	status.TrafficRoutingStatus = &transfergwv1beta1.TrafficRoutingStatus{
		Gateway: percentage,
		Ingress: 100 - percentage,
	}
	status.RollbackReady = converted > 0

	switch {
	case breach != nil:
		status.Phase = phaseRolledBack
		status.RollbackGeneration = migration.Generation
		msg := fmt.Sprintf("Rolled back to %d%%: %s", rollbackPercentage, breach)
		setCondition(status, "Ready", metav1.ConditionFalse, "HealthRollback", msg)
		setCondition(status, conditionRolledBack, metav1.ConditionTrue, "ThresholdBreached", msg)
		r.sendRollbackAlert(ctx, migration, msg)
	case awaitingPreRollout:
		status.Phase = phasePending
		setCondition(status, "Ready", metav1.ConditionFalse, "AwaitingPreRolloutHook",
			"Conversion is complete; waiting for the PreRollout hook to approve moving traffic")
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
		r.runHook(ctx, migration, status, lifecycle.PostRollout, false)
	default:
		status.Phase = phaseCanary
		setCondition(status, "Ready", metav1.ConditionFalse, "RolloutInProgress",
			fmt.Sprintf("Gateway is receiving %d%% of traffic", percentage))
	}

	if breach == nil && hasCondition(status, conditionRolledBack) {
		status.RollbackGeneration = 0
		setCondition(status, conditionRolledBack, metav1.ConditionFalse, "Resumed",
			"Rollout resumed after the spec was edited")
	}

	now := metav1.Now()
	status.LastTransitionTime = &now

	if err := r.patchStatus(ctx, migration, status); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	if status.Phase == phaseComplete || status.Phase == phaseFailed || status.Phase == phaseRolledBack {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

const maxIssues = 100

// SelectIngresses returns the Ingresses matching a TransferGW's selector.
//
// Exported (and taking a plain client.Client rather than a Reconciler) so the
// preview CLI (cmd/preview) can run the exact same selection logic the
// controller uses, against a real cluster, without a manager or a persisted
// TransferGW object.
func SelectIngresses(
	ctx context.Context,
	c client.Client,
	sel transfergwv1beta1.SelectorSpec,
) ([]networkingv1.Ingress, error) {
	labelSelector := labels.Everything()
	if sel.IngressSelector != nil {
		s, err := metav1.LabelSelectorAsSelector(sel.IngressSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid ingressSelector: %w", err)
		}
		labelSelector = s
	}

	namespaces, err := resolveNamespaces(ctx, c, sel.Namespaces)
	if err != nil {
		return nil, err
	}

	var out []networkingv1.Ingress
	for _, ns := range namespaces {
		list := &networkingv1.IngressList{}
		if err := c.List(ctx, list,
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
func resolveNamespaces(ctx context.Context, c client.Client, patterns []string) ([]string, error) {
	nsList := &corev1.NamespaceList{}
	if err := c.List(ctx, nsList); err != nil {
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
		gw.Spec.Listeners = BuildListeners(ingresses)

		// An ownerRef only works when the Gateway shares the migration's
		// namespace; Kubernetes does not garbage collect across namespaces.
		if namespace == migration.Namespace {
			return controllerutil.SetControllerReference(migration, gw, r.Scheme)
		}
		return nil
	})
	return err
}

// BuildListeners derives the Gateway listeners from the selected Ingresses.
// Routes from every namespace are allowed, since generated HTTPRoutes live
// alongside their source Ingress rather than in the Gateway's namespace.
func BuildListeners(ingresses []networkingv1.Ingress) []gatewayv1.Listener {
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

// conditionRolledBack surfaces a health rollback in kubectl describe, separate
// from Ready so the reason survives later Ready transitions.
const conditionRolledBack = "RolledBack"

// evaluateHealth compares the Ingress and Gateway paths and reports the first
// breached threshold. It returns nil whenever health checking does not apply:
// monitoring disabled, no thresholds configured, no metrics source wired up,
// the check interval not elapsed, or the backend being unreachable.
func (r *TransferGWReconciler) evaluateHealth(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
	target health.Target,
	status *transfergwv1beta1.TransferGWStatus,
) *health.Breach {
	logger := log.FromContext(ctx)

	monitoring := migration.Spec.Monitoring
	if monitoring == nil || !monitoring.Enabled || monitoring.Thresholds == nil {
		return nil
	}
	if r.MetricsSource == nil {
		r.warnNoMetricsSource.Do(func() {
			logger.Info("monitoring is enabled but the controller has no metrics source; " +
				"health-based rollback is disabled")
		})
		return nil
	}
	if status.NextHealthCheck != nil && time.Now().Before(status.NextHealthCheck.Time) {
		return nil
	}

	comparison, err := r.MetricsSource.Compare(ctx, migration, target)
	if err != nil {
		// An unreachable metrics backend is not evidence of an unhealthy
		// Gateway, so it must not move traffic.
		logger.Error(err, "comparing migration health, leaving the rollout where it is")
		return nil
	}

	now := metav1.Now()
	next := metav1.NewTime(now.Add(healthCheckInterval(monitoring)))
	status.LastHealthCheck = &now
	status.NextHealthCheck = &next
	status.MetricsStatus = toMetricsStatus(comparison)

	return health.Evaluate(monitoring.Thresholds, comparison)
}

func healthCheckInterval(monitoring *transfergwv1beta1.MonitoringSpec) time.Duration {
	if monitoring.Interval != "" {
		if d, err := time.ParseDuration(monitoring.Interval); err == nil && d > 0 {
			return d
		}
	}
	return defaultHealthCheckInterval
}

func toMetricsStatus(c *health.Comparison) *transfergwv1beta1.MetricsStatus {
	if c == nil {
		return nil
	}
	out := &transfergwv1beta1.MetricsStatus{
		ErrorRate:  metricComparison(c.ErrorRate, ""),
		Latency:    metricComparison(c.LatencyMs, "ms"),
		Throughput: metricComparison(c.Throughput, ""),
	}
	if out.ErrorRate == nil && out.Latency == nil && out.Throughput == nil {
		return nil
	}
	return out
}

func metricComparison(m *health.Metric, unit string) *transfergwv1beta1.MetricComparison {
	if m == nil {
		return nil
	}
	status := "degraded"
	if m.Gateway <= m.Ingress {
		status = "healthy"
	}
	return &transfergwv1beta1.MetricComparison{
		Ingress: fmt.Sprintf("%g%s", m.Ingress, unit),
		Gateway: fmt.Sprintf("%g%s", m.Gateway, unit),
		Delta:   fmt.Sprintf("%+g%s", m.Gateway-m.Ingress, unit),
		Status:  status,
	}
}

func ingressNamespaces(ingresses []networkingv1.Ingress) []string {
	seen := map[string]struct{}{}
	var out []string
	for i := range ingresses {
		ns := ingresses[i].Namespace
		if _, dup := seen[ns]; dup {
			continue
		}
		seen[ns] = struct{}{}
		out = append(out, ns)
	}
	return out
}

func hasCondition(status *transfergwv1beta1.TransferGWStatus, condType string) bool {
	for i := range status.Conditions {
		if status.Conditions[i].Type == condType {
			return true
		}
	}
	return false
}

// sendRollbackAlert notifies a webhook about a health rollback. A migration
// that sets spec.monitoring.alerting is always honored exactly as written
// (including an explicit Enabled: false, which must suppress alerting even
// when a cluster-wide default is configured). A migration that leaves
// alerting unset falls back to DefaultWebhookURL, so a cluster operator can
// set one default in the chart instead of repeating it on every TransferGW.
//
// A delivery failure is logged, not returned: the rollback already happened
// and is already recorded in status, so a webhook that's down or rejects the
// request should not make Reconcile report an error and get retried with
// backoff over something reconciling can't fix.
func (r *TransferGWReconciler) sendRollbackAlert(ctx context.Context, migration *transfergwv1beta1.TransferGW, message string) {
	if r.Alerter == nil {
		return
	}

	webhookURL := r.DefaultWebhookURL
	if m := migration.Spec.Monitoring; m != nil && m.Alerting != nil {
		if !m.Alerting.Enabled {
			return
		}
		webhookURL = m.Alerting.WebhookUrl
	}
	if webhookURL == "" {
		return
	}

	err := r.Alerter.Notify(ctx, webhookURL, alert.Event{
		Migration: migration.Namespace + "/" + migration.Name,
		Phase:     phaseRolledBack,
		Reason:    "ThresholdBreached",
		Message:   message,
		Time:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "sending rollback alert webhook")
	}
}

// rollbackReason recovers the recorded breach message for logging while held.
func rollbackReason(migration *transfergwv1beta1.TransferGW) string {
	for i := range migration.Status.Conditions {
		if migration.Status.Conditions[i].Type == conditionRolledBack {
			return migration.Status.Conditions[i].Message
		}
	}
	return ""
}

// resolveServicePort looks up the numeric port for a Service backend
// referenced by name, so an Ingress using a named port converts instead of
// failing outright - the port name has to be resolved against something,
// and the controller is the only place with a live client to do that.
func (r *TransferGWReconciler) resolveServicePort(ctx context.Context) conversion.PortResolver {
	return func(namespace, serviceName, portName string) (int32, error) {
		svc := &corev1.Service{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serviceName}, svc); err != nil {
			return 0, err
		}
		for _, p := range svc.Spec.Ports {
			if p.Name == portName {
				return p.Port, nil
			}
		}
		return 0, fmt.Errorf("service %s/%s has no port named %q", namespace, serviceName, portName)
	}
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

func findCondition(status *transfergwv1beta1.TransferGWStatus, condType string) *metav1.Condition {
	for i := range status.Conditions {
		if status.Conditions[i].Type == condType {
			return &status.Conditions[i]
		}
	}
	return nil
}

// hookConditionType names the condition a hook stage's outcome is recorded
// under, so kubectl describe shows which stage a migration is held on.
func hookConditionType(stage lifecycle.Stage) string {
	return "Hook" + string(stage)
}

// hookFor returns the webhook configured for stage, or nil if lifecycle hooks
// aren't configured at all or that particular stage is unset.
func hookFor(migration *transfergwv1beta1.TransferGW, stage lifecycle.Stage) *transfergwv1beta1.WebhookSpec {
	h := migration.Spec.Lifecycle
	if h == nil || h.Hooks == nil {
		return nil
	}
	switch stage {
	case lifecycle.PreConversion:
		return h.Hooks.PreConversion
	case lifecycle.PostConversion:
		return h.Hooks.PostConversion
	case lifecycle.PreRollout:
		return h.Hooks.PreRollout
	case lifecycle.PostRollout:
		return h.Hooks.PostRollout
	default:
		return nil
	}
}

// runHook resolves stage's hook (if any) for the current spec generation and
// reports whether the migration may proceed past that stage.
//
// A hook's outcome is recorded as a condition stamped with the generation it
// was evaluated against, so an already-approved hook is not called again on
// every reconcile - only once per generation, i.e. only again once the spec
// changes. blocking hooks (PreConversion, PreRollout) return the hook's
// answer; non-blocking hooks (PostConversion, PostRollout) always return true
// since there's nothing left at that point to hold back - they only notify.
func (r *TransferGWReconciler) runHook(
	ctx context.Context,
	migration *transfergwv1beta1.TransferGW,
	status *transfergwv1beta1.TransferGWStatus,
	stage lifecycle.Stage,
	blocking bool,
) bool {
	hook := hookFor(migration, stage)
	if hook == nil || hook.Url == "" {
		return true
	}

	condType := hookConditionType(stage)
	if c := findCondition(status, condType); c != nil &&
		c.ObservedGeneration == migration.Generation && c.Status == metav1.ConditionTrue {
		return true
	}

	if r.HookCaller == nil {
		r.warnNoHookCaller.Do(func() {
			log.FromContext(ctx).Info(
				"spec.lifecycle.hooks is configured but no HookCaller is wired; hooks are treated as approved",
				"stage", stage)
		})
		return true
	}

	proceed, err := r.HookCaller.Call(ctx, hook.Url, lifecycle.Event{
		Migration: migration.Namespace + "/" + migration.Name,
		Stage:     string(stage),
		Time:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "calling lifecycle hook", "stage", stage)
	}

	reason, msg, condStatus := "Approved", fmt.Sprintf("%s hook approved", stage), metav1.ConditionTrue
	if !proceed {
		reason, msg, condStatus = "Blocked", fmt.Sprintf("%s hook has not approved yet", stage), metav1.ConditionFalse
	}
	setCondition(status, condType, condStatus, reason, msg)
	if c := findCondition(status, condType); c != nil {
		c.ObservedGeneration = migration.Generation
	}

	if !blocking {
		return true
	}
	return proceed
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

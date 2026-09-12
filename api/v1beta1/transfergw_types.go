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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TransferGWSpec defines the desired state of TransferGW
type TransferGWSpec struct {
	// Selector specifies which Ingress resources to migrate
	// +kubebuilder:validation:Required
	Selector SelectorSpec `json:"selector"`

	// Conversion specifies how to translate Ingress to Gateway API
	// +kubebuilder:validation:Required
	Conversion ConversionSpec `json:"conversion"`

	// Rollout specifies the migration rollout strategy
	// +kubebuilder:validation:Required
	Rollout RolloutSpec `json:"rollout"`

	// Monitoring specifies health checks and rollback thresholds
	// +kubebuilder:validation:Optional
	Monitoring *MonitoringSpec `json:"monitoring,omitempty"`

	// Lifecycle specifies cleanup and validation behavior
	// +kubebuilder:validation:Optional
	Lifecycle *LifecycleSpec `json:"lifecycle,omitempty"`
}

// SelectorSpec specifies which Ingress resources to select
type SelectorSpec struct {
	// Namespaces is a list of namespace patterns (glob supported)
	// +kubebuilder:validation:Optional
	Namespaces []string `json:"namespaces,omitempty"`

	// IngressSelector is a label selector for Ingress resources
	// +kubebuilder:validation:Optional
	IngressSelector *metav1.LabelSelector `json:"ingressSelector,omitempty"`

	// IngressClasses specifies specific ingress classes to migrate
	// +kubebuilder:validation:Optional
	IngressClasses []string `json:"ingressClasses,omitempty"`
}

// ConversionSpec specifies how to convert Ingress to Gateway API
type ConversionSpec struct {
	// GatewayClass is the target gateway class
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	GatewayClass string `json:"gatewayClass"`

	// TargetNamespace is where Gateway resources are created
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default="transfergw"
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// GenerateGateway auto-creates Gateway resource if missing
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default=true
	GenerateGateway bool `json:"generateGateway,omitempty"`

	// AnnotationPolicy specifies how to handle ingress annotations
	// +kubebuilder:validation:Optional
	AnnotationPolicy *AnnotationPolicySpec `json:"annotationPolicy,omitempty"`

	// TLSHandling specifies how to handle TLS certificates
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=preserve;regenerate;manual
	// +kubebuilder:validation:Default=preserve
	TLSHandling string `json:"tlsHandling,omitempty"`

	// ValidationMode specifies validation strictness
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=strict;permissive;disabled
	// +kubebuilder:validation:Default=strict
	ValidationMode string `json:"validationMode,omitempty"`
}

// AnnotationPolicySpec specifies annotation handling during conversion
type AnnotationPolicySpec struct {
	// Preserve is a list of regex patterns of annotations to preserve
	// +kubebuilder:validation:Optional
	Preserve []string `json:"preserve,omitempty"`

	// Translate maps old annotation names to new policy names
	// +kubebuilder:validation:Optional
	Translate map[string]string `json:"translate,omitempty"`

	// Drop is a list of regex patterns of annotations to remove
	// +kubebuilder:validation:Optional
	Drop []string `json:"drop,omitempty"`
}

// RolloutSpec specifies the migration rollout strategy
type RolloutSpec struct {
	// Mode is the rollout mode
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=immediate;gradual;canary
	Mode string `json:"mode"`

	// Strategy is the rollout strategy
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=percentage;time-based;manual
	// +kubebuilder:validation:Default=percentage
	Strategy string `json:"strategy,omitempty"`

	// Paused pauses the rollout without deletion
	// +kubebuilder:validation:Optional
	Paused bool `json:"paused,omitempty"`

	// Canary specifies canary-specific rollout settings
	// +kubebuilder:validation:Optional
	Canary *CanarySpec `json:"canary,omitempty"`

	// Gradual specifies gradual rollout settings
	// +kubebuilder:validation:Optional
	Gradual *GradualSpec `json:"gradual,omitempty"`
}

// CanarySpec specifies canary rollout settings
type CanarySpec struct {
	// InitialPercentage is the initial traffic percentage
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Default=25
	InitialPercentage int32 `json:"initialPercentage,omitempty"`

	// Increment is the traffic percentage increase per step
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:validation:Default=25
	Increment int32 `json:"increment,omitempty"`

	// StepDuration is the duration between increment steps
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default="1h"
	StepDuration string `json:"stepDuration,omitempty"`

	// MaxDuration is the maximum total canary duration
	// +kubebuilder:validation:Optional
	MaxDuration string `json:"maxDuration,omitempty"`
}

// GradualSpec specifies gradual rollout settings
type GradualSpec struct {
	// TotalDuration is the total time to reach 100%
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default="24h"
	TotalDuration string `json:"totalDuration,omitempty"`

	// Step is the percentage step size
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default=10
	Step int32 `json:"step,omitempty"`
}

// MonitoringSpec specifies health checks and rollback thresholds
type MonitoringSpec struct {
	// Enabled enables health checks
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default=true
	Enabled bool `json:"enabled,omitempty"`

	// Interval is the health check interval
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default="5m"
	Interval string `json:"interval,omitempty"`

	// Metrics specifies which metrics to collect
	// +kubebuilder:validation:Optional
	Metrics []string `json:"metrics,omitempty"`

	// Thresholds specifies rollback thresholds
	// +kubebuilder:validation:Optional
	Thresholds *ThresholdSpec `json:"thresholds,omitempty"`

	// ComparisonWindow is the evaluation window for metrics
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default="5m"
	ComparisonWindow string `json:"comparisonWindow,omitempty"`

	// Alerting specifies alerting configuration
	// +kubebuilder:validation:Optional
	Alerting *AlertingSpec `json:"alerting,omitempty"`
}

// ThresholdSpec specifies rollback thresholds
type ThresholdSpec struct {
	// ErrorRate is the error rate threshold
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +kubebuilder:validation:Default=0.05
	ErrorRate *float64 `json:"errorRate,omitempty"`

	// LatencyMs is the latency threshold in milliseconds
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default=150
	LatencyMs int32 `json:"latencyMs,omitempty"`

	// LatencyPercentile is the latency percentile
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=p50;p75;p90;p95;p99
	// +kubebuilder:validation:Default=p95
	LatencyPercentile string `json:"latencyPercentile,omitempty"`

	// ConnectionResets is the connection resets threshold
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default=0.01
	ConnectionResets float64 `json:"connectionResets,omitempty"`

	// ThroughputDelta is the throughput delta percentage threshold
	// +kubebuilder:validation:Optional
	ThroughputDelta *float64 `json:"throughputDelta,omitempty"`
}

// AlertingSpec specifies alerting configuration
type AlertingSpec struct {
	// Enabled enables alerting
	// +kubebuilder:validation:Optional
	Enabled bool `json:"enabled,omitempty"`

	// WebhookUrl is the optional webhook URL for alerts
	// +kubebuilder:validation:Optional
	WebhookUrl string `json:"webhookUrl,omitempty"`

	// SlackChannel is the optional Slack channel
	// +kubebuilder:validation:Optional
	SlackChannel string `json:"slackChannel,omitempty"`
}

// LifecycleSpec specifies cleanup and validation behavior
type LifecycleSpec struct {
	// PauseOriginalIngress pauses original ingress during rollout
	// +kubebuilder:validation:Optional
	PauseOriginalIngress bool `json:"pauseOriginalIngress,omitempty"`

	// BackupOriginal keeps backup of original configuration
	// +kubebuilder:validation:Optional
	BackupOriginal bool `json:"backupOriginal,omitempty"`

	// ValidateConversion rejects if conversion has warnings
	// +kubebuilder:validation:Optional
	ValidateConversion bool `json:"validateConversion,omitempty"`

	// AutoComplete marks complete when reaching 100%
	// +kubebuilder:validation:Optional
	AutoComplete bool `json:"autoComplete,omitempty"`

	// CleanupOnSuccess deletes original ingress after migration
	// +kubebuilder:validation:Optional
	CleanupOnSuccess bool `json:"cleanupOnSuccess,omitempty"`

	// Hooks specifies pre/post migration webhooks
	// +kubebuilder:validation:Optional
	Hooks *HooksSpec `json:"hooks,omitempty"`
}

// HooksSpec specifies pre/post migration webhooks
type HooksSpec struct {
	// PreConversion runs before conversion
	// +kubebuilder:validation:Optional
	PreConversion *WebhookSpec `json:"preConversion,omitempty"`

	// PostConversion runs after conversion
	// +kubebuilder:validation:Optional
	PostConversion *WebhookSpec `json:"postConversion,omitempty"`

	// PreRollout runs before rollout
	// +kubebuilder:validation:Optional
	PreRollout *WebhookSpec `json:"preRollout,omitempty"`

	// PostRollout runs after rollout complete
	// +kubebuilder:validation:Optional
	PostRollout *WebhookSpec `json:"postRollout,omitempty"`
}

// WebhookSpec specifies a webhook configuration
type WebhookSpec struct {
	// Url is the webhook URL
	// +kubebuilder:validation:Required
	Url string `json:"url"`
}

// TransferGWStatus defines the observed state of TransferGW
type TransferGWStatus struct {
	// Phase is the current migration phase
	// +kubebuilder:validation:Enum=Pending;Analyzing;Converting;Deploying;Canary;Complete;Failed
	Phase string `json:"phase,omitempty"`

	// CompletionPercentage is the traffic percentage on gateway
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	CompletionPercentage int32 `json:"completionPercentage,omitempty"`

	// TrafficRouting shows current traffic distribution
	TrafficRoutingStatus *TrafficRoutingStatus `json:"trafficRouting,omitempty"`

	// Processed shows conversion statistics
	ProcessedStatus *ProcessedStatus `json:"processed,omitempty"`

	// Resources shows created gateway resources
	ResourcesStatus *ResourcesStatus `json:"resources,omitempty"`

	// Metrics shows comparison metrics
	MetricsStatus *MetricsStatus `json:"metrics,omitempty"`

	// Issues shows conversion warnings
	// +kubebuilder:validation:MaxItems=100
	Issues []ConversionIssue `json:"issues,omitempty"`

	// RollbackReady indicates if rollback is ready
	RollbackReady bool `json:"rollbackReady,omitempty"`

	// LastHealthCheck is the last health check time
	LastHealthCheck *metav1.Time `json:"lastHealthCheck,omitempty"`

	// NextHealthCheck is the next scheduled health check
	NextHealthCheck *metav1.Time `json:"nextHealthCheck,omitempty"`

	// LastTransitionTime is the last status transition time
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// Conditions is a list of conditions
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TrafficRoutingStatus shows current traffic distribution
type TrafficRoutingStatus struct {
	Ingress int32 `json:"ingress,omitempty"`
	Gateway int32 `json:"gateway,omitempty"`
}

// ProcessedStatus shows conversion statistics
type ProcessedStatus struct {
	Total     int32 `json:"total,omitempty"`
	Converted int32 `json:"converted,omitempty"`
	Pending   int32 `json:"pending,omitempty"`
	Failed    int32 `json:"failed,omitempty"`
}

// ResourcesStatus shows created gateway resources
type ResourcesStatus struct {
	Gateways        int32 `json:"gateways,omitempty"`
	HTTPRoutes      int32 `json:"httpRoutes,omitempty"`
	TLSPolicies     int32 `json:"tlsPolicies,omitempty"`
	BackendPolicies int32 `json:"backendPolicies,omitempty"`
}

// MetricsStatus shows comparison metrics
type MetricsStatus struct {
	Latency    *MetricComparison `json:"latency,omitempty"`
	ErrorRate  *MetricComparison `json:"errorRate,omitempty"`
	Throughput *MetricComparison `json:"throughput,omitempty"`
}

// MetricComparison shows metric comparison between ingress and gateway
type MetricComparison struct {
	Ingress string `json:"ingress,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Delta   string `json:"delta,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ConversionIssue represents a conversion warning or error
type ConversionIssue struct {
	Ingress        string `json:"ingress,omitempty"`
	Issue          string `json:"issue,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tgw
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Progress",type=integer,JSONPath=`.status.completionPercentage`
// +kubebuilder:printcolumn:name="Ingress",type=integer,JSONPath=`.status.trafficRouting.ingress`
// +kubebuilder:printcolumn:name="Gateway",type=integer,JSONPath=`.status.trafficRouting.gateway`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TransferGW is the Schema for the transfergws API
type TransferGW struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TransferGWSpec   `json:"spec,omitempty"`
	Status TransferGWStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TransferGWList contains a list of TransferGW
type TransferGWList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TransferGW `json:"items"`
}

// TransferGW and TransferGWList are registered with the scheme by
// addKnownTypes in groupversion_info.go.

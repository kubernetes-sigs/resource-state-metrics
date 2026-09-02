/*
Copyright 2026 The Kubernetes resource-state-metrics Authors.

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

package v1alpha1

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (

	// ConditionTypeProcessed represents the condition type for a resource that has been processed successfully.
	ConditionTypeProcessed = iota

	// ConditionTypeFailed represents the condition type for resource that has failed to process further.
	ConditionTypeFailed

	// ConditionTypeCardinalityWarning represents the condition type when cardinality reaches warning threshold (80% default).
	ConditionTypeCardinalityWarning

	// ConditionTypeCardinalityCutoff represents the condition type when cardinality exceeds hard threshold (100%).
	ConditionTypeCardinalityCutoff
)

var (

	// ConditionType is a slice of strings representing the condition types.
	ConditionType = []string{"Processed", "Failed", "CardinalityWarning", "CardinalityCutoff"}

	// ConditionMessageTrue is a group of condition messages applicable when the associated condition status is true.
	ConditionMessageTrue = []string{
		"Resource configuration has been processed successfully",
		"Resource failed to process",
		"Cardinality is approaching threshold",
		"Cardinality threshold exceeded, metric generation cut off",
	}

	// ConditionMessageFalse is a group of condition messages applicable when the associated condition status is false.
	ConditionMessageFalse = []string{
		"Resource configuration is yet to be processed",
		"N/A",
		"Cardinality is within acceptable limits",
		"Cardinality is within acceptable limits",
	}

	// ConditionReasonTrue is a group of condition reasons applicable when the associated condition status is true.
	ConditionReasonTrue = []string{"EventHandlerSucceeded", "EventHandlerFailed", "CardinalityWarning", "CardinalityCutoff"}

	// ConditionReasonFalse is a group of condition reasons applicable when the associated condition status is false.
	ConditionReasonFalse = []string{"EventHandlerRunning", "N/A", "CardinalityOK", "CardinalityOK"}
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:singular=resourcemetricsmonitor,scope=Namespaced,shortName=rmm
// +kubebuilder:rbac:groups=resource-state-metrics.instrumentation.k8s-sigs.io,resources=resourcemetricsmonitors;resourcemetricsmonitors/status,verbs=*
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create
// +kubebuilder:subresource:status

// ResourceMetricsMonitor is a specification for a ResourceMetricsMonitor resource.
type ResourceMetricsMonitor struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec defines the desired state of ResourceMetricsMonitor.
	// +required
	Spec ResourceMetricsMonitorSpec `json:"spec"`
	// status defines the observed state of ResourceMetricsMonitor.
	// +optional
	Status ResourceMetricsMonitorStatus `json:"status,omitempty"`
}

// ResolverType represents the type of resolver to use for label/value expressions.
// +kubebuilder:validation:Enum=cel;unstructured;starlark;""
type ResolverType string

const (
	// ResolverTypeCEL uses Common Expression Language (CEL) to evaluate expressions.
	ResolverTypeCEL ResolverType = "cel"
	// ResolverTypeUnstructured uses simple dot notation to resolve expressions.
	ResolverTypeUnstructured ResolverType = "unstructured"
	// ResolverTypeStarlark uses Starlark scripts for complex metric generation.
	ResolverTypeStarlark ResolverType = "starlark"
	// ResolverTypeNone represents "inherit from parent" for Family/Metric resolver fields.
	ResolverTypeNone ResolverType = ""
)

// Label directly associates a label name with its value expression.
type Label struct {
	// name is the label name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +required
	Name string `json:"name"`

	// value is the expression to evaluate for this label's value.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +required
	Value string `json:"value"`
}

// StarlarkConfig configures Starlark script execution for metric generation.
type StarlarkConfig struct {
	// script is the inline Starlark script source code.
	// The script has access to `obj` (the resource as a dict) and built-in functions:
	// - quantity_to_float(s): Parse Kubernetes Quantity ("100m", "1Gi") to float
	// - time: starlark-go's time module (https://pkg.go.dev/go.starlark.net/lib/time).
	//   Notable entries: time.now() returns the current time.time; time.parse_time(s)
	//   parses an RFC-3339 string (e.g. condition.lastTransitionTime) into a time.time;
	//   subtracting two time.time values yields a time.duration whose .seconds
	//   attribute is a float. Example duration-since-transition pattern:
	//   `(time.now() - time.parse_time(c["lastTransitionTime"])).seconds`.
	//   Note: time.parse_time errors on empty input, so guard with
	//   `if not ts: continue` before calling it.
	// - label_prefix(prefix, labels): Prefix and sanitize a label dict's keys
	// - metric(labels, value): Create a sample with labels dict and float value
	// - family(name, help, kind, samples): Create a family with list of samples
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=65536
	// +required
	Script string `json:"script"`

	// timeout is the maximum execution time in seconds (default: 5, max: 60).
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=60
	Timeout int32 `json:"timeout,omitempty"`

	// maxSteps limits the number of Starlark execution steps (default: 100000).
	// This prevents infinite loops and runaway scripts.
	// +optional
	// +kubebuilder:validation:Minimum=1000
	MaxSteps int32 `json:"maxSteps,omitempty"`
}

// Metric represents a single time series within a family.
type Metric struct {
	// labels defines the label set for this metric.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=64
	Labels []Label `json:"labels,omitempty"`

	// value is the expression to evaluate for the metric value.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +required
	Value string `json:"value"`

	// resolver overrides the family/store resolver for this metric.
	// +optional
	Resolver ResolverType `json:"resolver,omitempty"`
}

// Family represents a metric family (a group of metrics with the same name).
type Family struct {
	// name is the metric family name (will be prefixed with kube_customresource_).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-zA-Z_][a-zA-Z0-9_]*$`
	// +required
	Name string `json:"name"`

	// help is the help text for this metric family.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +required
	Help string `json:"help"`

	// metrics defines the individual metrics within this family.
	// When Starlark is set, this field is ignored.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=128
	Metrics []Metric `json:"metrics,omitempty"`

	// starlark configures Starlark script-based metric generation.
	// When set, the script generates all samples for this family,
	// bypassing the normal Metrics/Resolver pipeline.
	// +optional
	Starlark *StarlarkConfig `json:"starlark,omitempty"`

	// resolver overrides the store resolver for this family.
	// +optional
	Resolver ResolverType `json:"resolver,omitempty"`

	// labels defines additional labels to apply to all metrics in this family.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=64
	Labels []Label `json:"labels,omitempty"`

	// cardinalityLimit sets the maximum cardinality for this family (0 means unlimited).
	// +optional
	// +kubebuilder:validation:Minimum=0
	CardinalityLimit int64 `json:"cardinalityLimit,omitempty"`
}

// Selectors defines label and field selectors for filtering resources.
// +kubebuilder:validation:MinProperties=1
type Selectors struct {
	// label is a label selector for filtering resources.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Label string `json:"label,omitempty"`

	// field is a field selector for filtering resources.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Field string `json:"field,omitempty"`
}

// Store defines how to generate metrics for a specific resource type.
// GVK fields support wildcards ("*") to match multiple resources via API discovery.
// When using wildcards, the controller will auto-discover matching resources.
type Store struct {
	// group is the API group of the resource (empty string for core resources).
	// Supports "*" to match all groups.
	// +optional
	// +kubebuilder:validation:MinLength=0
	// +kubebuilder:validation:MaxLength=253
	Group string `json:"group,omitempty"`

	// version is the API version of the resource. Supports "*" to match all versions.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +required
	Version string `json:"version"`

	// kind is the kind of the resource.
	// Supports "*" to match all kinds within the specified group/version.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +required
	Kind string `json:"kind"`

	// resource is the plural resource name (e.g. "deployments", "pods").
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Resource string `json:"resource,omitempty"`

	// selectors defines how to filter the resources to watch.
	// +optional
	Selectors Selectors `json:"selectors,omitempty"`

	// families defines the metric families to generate for this resource.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=128
	// +required
	// +listType=atomic
	Families []Family `json:"families"`

	// resolver sets the default resolver for all families/metrics in this store.
	// If not specified, must be set at the family or metric level.
	// +optional
	Resolver ResolverType `json:"resolver,omitempty"`

	// labels defines additional labels to apply to all metrics in this store.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=64
	Labels []Label `json:"labels,omitempty"`

	// cardinalityLimit sets the maximum cardinality for this store (0 means use default).
	// +optional
	// +kubebuilder:validation:Minimum=0
	CardinalityLimit int64 `json:"cardinalityLimit,omitempty"`
}

// Configuration defines the metric generation configuration.
type Configuration struct {
	// stores defines the resources to watch and the metrics to generate.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=128
	// +required
	// +listType=atomic
	Stores []Store `json:"stores"`

	// cardinalityLimit sets the maximum total cardinality for this RMM (0 means use global default).
	// +optional
	// +kubebuilder:validation:Minimum=0
	CardinalityLimit int64 `json:"cardinalityLimit,omitempty"`
}

// ResourceMetricsMonitorSpec is the spec for a ResourceMetricsMonitor resource.
type ResourceMetricsMonitorSpec struct {
	// configuration is the RSM configuration that generates metrics.
	// +required
	Configuration Configuration `json:"configuration"`
}

// ResourceMetricsMonitorStatus is the status for a ResourceMetricsMonitor resource.
// +kubebuilder:validation:MinProperties=0
type ResourceMetricsMonitorStatus struct {
	// conditions is an array of conditions associated with the resource.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// cardinality tracks the cardinality metrics for this resource.
	// +optional
	Cardinality *CardinalityStatus `json:"cardinality,omitempty"`
}

// CardinalityStatus tracks cardinality information for the RMM resource.
type CardinalityStatus struct {
	// total is the total number of time series generated by this RMM.
	// +kubebuilder:validation:Minimum=0
	// +required
	Total int64 `json:"total"`

	// perStore maps store identifiers (group/version/kind) to their cardinality.
	// +optional
	// +kubebuilder:validation:MinProperties=0
	PerStore map[string]int64 `json:"perStore,omitempty"`

	// perFamily maps family names to their cardinality.
	// +optional
	// +kubebuilder:validation:MinProperties=0
	PerFamily map[string]int64 `json:"perFamily,omitempty"`

	// thresholdsExceeded indicates whether any cardinality threshold has been exceeded.
	// +required
	ThresholdsExceeded bool `json:"thresholdsExceeded"`

	// cutoffFamilies lists the families that are currently cut off due to threshold violations.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=128
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	CutoffFamilies []string `json:"cutoffFamilies,omitempty"`

	// lastUpdated is the timestamp of the last cardinality update.
	// +optional
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
}

// Set sets the given condition for the resource.
func (status *ResourceMetricsMonitorStatus) Set(
	resource *ResourceMetricsMonitor,
	condition metav1.Condition,
) {
	// Prefix condition messages with consistent hints.
	var message, reason string

	conditionTypeNumeric := slices.Index(ConditionType, condition.Type)
	if condition.Status == metav1.ConditionTrue {
		reason = ConditionReasonTrue[conditionTypeNumeric]
		message = ConditionMessageTrue[conditionTypeNumeric]
	} else {
		reason = ConditionReasonFalse[conditionTypeNumeric]
		message = ConditionMessageFalse[conditionTypeNumeric]
	}

	// Allow the caller to override the default reason (e.g. CardinalityCutoff=False
	// when a warning is active should carry "CardinalityWarning", not "CardinalityOK").
	if condition.Reason != "" {
		reason = condition.Reason
	}

	// Populate status fields.
	condition.Reason = reason
	condition.Message = message
	condition.LastTransitionTime = metav1.Now()
	condition.ObservedGeneration = resource.GetGeneration()

	// Check if the condition already exists.
	for i, existingCondition := range status.Conditions {
		if existingCondition.Type == condition.Type {
			// Update the existing condition.
			status.Conditions[i] = condition

			return
		}
	}

	// Append the new condition if it does not exist (+listMapKey=type).
	status.Conditions = append(status.Conditions, condition)
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// ResourceMetricsMonitorList is a list of ResourceMetricsMonitor resources.
type ResourceMetricsMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard list metadata.
	metav1.ListMeta `json:"metadata"`

	// items is the list of ResourceMetricsMonitor objects.
	Items []ResourceMetricsMonitor `json:"items"`
}

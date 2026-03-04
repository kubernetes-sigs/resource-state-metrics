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

package internal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/internal/version"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

// revisionSHARegex extracts the git revision from version.Version() output.
var revisionSHARegex = regexp.MustCompile(`revision:\s*(\S+)\)`) //nolint:forbidigo // package-level

type eventType int

const (
	addEvent eventType = iota
	updateEvent
	deleteEvent

	// cardinalityPollInterval is how often the cardinality poller checks for store metrics before computing cardinality status.
	// NOTE This should be small enough to catch high-cardinality scenarios in a timely manner, but not so small as to cause excessive API calls or CPU usage while waiting for metrics to appear.
	cardinalityPollInterval = 500 * time.Millisecond
	// cardinalityPollTimeout is the maximum time to wait for store metrics to appear before computing cardinality status anyway.
	cardinalityPollTimeout = 5 * time.Second
)

func (e eventType) String() string {
	return []string{"addEvent", "updateEvent", "deleteEvent"}[e]
}

func (c *Controller) handleEvent(ctx context.Context, stores *sync.Map, event string, o metav1.Object) error {
	logger := klog.FromContext(ctx)

	resource, err := c.validateAndPrepareResource(ctx, o, event)
	if err != nil {
		logger.Error(err, "resource validation and preparation failed")
		c.eventsProcessed.WithLabelValues(o.GetNamespace(), o.GetName(), event, "failed").Inc()

		return nil
	}

	// emitDone is closed after emitSuccess completes so that the cardinality
	// goroutine (started inside processAddOrUpdate) can safely update the
	// resource status without racing with emitSuccess.
	emitDone := make(chan struct{})
	defer close(emitDone)

	if err := c.processEvent(ctx, stores, event, resource, emitDone); err != nil {
		logger.Error(err, "event processing failed")
		c.eventsProcessed.WithLabelValues(resource.GetNamespace(), resource.GetName(), event, "failed").Inc()

		return nil
	}

	if _, err := c.emitSuccess(ctx, resource, metav1.ConditionTrue, fmt.Sprintf("Event handler successfully processed event: %s", event)); err != nil {
		logger.Error(fmt.Errorf("failed to emit success on %s: %w", klog.KObj(resource).String(), err), "cannot update the resource")
		c.eventsProcessed.WithLabelValues(resource.GetNamespace(), resource.GetName(), event, "failed").Inc()

		return nil
	}

	c.eventsProcessed.WithLabelValues(resource.GetNamespace(), resource.GetName(), event, "success").Inc()

	return nil
}

func (c *Controller) validateAndPrepareResource(ctx context.Context, o metav1.Object, event string) (*v1alpha1.ResourceMetricsMonitor, error) {
	logger := klog.FromContext(ctx)

	resource, ok := o.(*v1alpha1.ResourceMetricsMonitor)
	if !ok {
		logger.Error(errors.New("failed to cast object to ResourceMetricsMonitor"), "cannot handle event")

		return nil, errors.New("invalid object type")
	}

	if err := c.updateMetadata(ctx, resource); err != nil {
		logger.Error(fmt.Errorf("failed to update metadata for %s: %w", klog.KObj(resource).String(), err), "cannot handle event")

		return nil, err
	}

	updatedResource, err := c.emitSuccess(ctx, resource, metav1.ConditionFalse, fmt.Sprintf("Event handler received event: %s", event))
	if err != nil {
		logger.Error(fmt.Errorf("failed to emit success on %s: %w", klog.KObj(resource).String(), err), "cannot update the resource")

		return nil, err
	}

	return updatedResource, nil
}

func (c *Controller) processEvent(ctx context.Context, stores *sync.Map, event string, resource *v1alpha1.ResourceMetricsMonitor, emitDone <-chan struct{}) error {
	switch event {
	case addEvent.String(), updateEvent.String():
		return c.processAddOrUpdate(ctx, stores, event, resource, emitDone)
	case deleteEvent.String():
		return c.processDelete(stores, resource)
	default:
		logger := klog.FromContext(ctx)
		logger.Error(fmt.Errorf("unknown event type (%s)", event), "cannot process the resource")
		c.emitFailure(ctx, resource, fmt.Sprintf("Unknown event type: %s", event))
		c.eventsProcessed.WithLabelValues(resource.GetNamespace(), resource.GetName(), event, "failed").Inc()

		return fmt.Errorf("unknown event type: %s", event)
	}
}

func (c *Controller) processAddOrUpdate(ctx context.Context, stores *sync.Map, _ string, resource *v1alpha1.ResourceMetricsMonitor, emitDone <-chan struct{}) error {
	stores.Delete(resource.GetUID())

	configurerInstance := newConfigurer(
		c.dynamicClientset,
		resource,
		*c.options.CELCostLimit,
		time.Duration(*c.options.CELTimeout)*time.Second,
		c.celEvaluations,
		*c.options.ResourceCardinalityDefault,
		*c.options.CardinalityWarningRatio,
		time.Duration(*c.options.StarlarkTimeout)*time.Second,
		*c.options.StarlarkMaxSteps,
	)

	configurerInstance.build(ctx, stores)
	c.resourcesMonitored.WithLabelValues(resource.GetNamespace(), resource.GetName()).Set(1)

	// Non-blocking wait to allow metrics to be generated before calculating cardinality.
	// The goroutine waits for all store reflectors to complete their initial list (synced)
	// so that cardinality is calculated with complete data rather than partial results.
	// It then waits on emitDone to ensure emitSuccess has completed its status update
	// before persistCardinalityStatus runs, preventing concurrent Get-Modify-Update
	// cycles from overwriting each other's changes.
	go func() {
		_ = wait.PollUntilContextTimeout(ctx, cardinalityPollInterval, cardinalityPollTimeout, true, func(_ context.Context) (bool, error) {
			storesI, ok := stores.Load(resource.GetUID())
			if !ok {
				return false, nil
			}

			storesList, ok := storesI.([]*StoreType)
			if !ok || len(storesList) == 0 {
				return false, nil
			}

			for _, store := range storesList {
				if !store.IsSynced() {
					return false, nil
				}
			}

			return true, nil
		})

		<-emitDone

		if err := c.updateCardinalityStatus(ctx, resource); err != nil {
			klog.FromContext(ctx).Error(err, "failed to update cardinality status")
		}
	}()

	return nil
}

func (c *Controller) processDelete(stores *sync.Map, resource *v1alpha1.ResourceMetricsMonitor) error {
	stores.Delete(resource.GetUID())
	c.resourcesMonitored.DeleteLabelValues(resource.GetNamespace(), resource.GetName())

	// Clean up cardinality tracking
	c.globalCardinalityManager.DeleteResource(resource.GetUID())

	// Clean up cardinality metrics
	c.resourceCardinality.DeleteLabelValues(resource.GetNamespace(), resource.GetName())
	// Note: Per-store/per-family metrics are not cleaned up here as it would require
	// iterating through all label combinations. They will be overwritten if the RMM is recreated.

	// Update global cardinality metric
	c.globalCardinality.Set(float64(c.globalCardinalityManager.GetGlobalTotal()))

	return nil
}

func (c *Controller) emitSuccess(ctx context.Context, monitor *v1alpha1.ResourceMetricsMonitor, statusBool metav1.ConditionStatus, message string) (*v1alpha1.ResourceMetricsMonitor, error) {
	kObj := klog.KObj(monitor).String()

	resource, err := c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(monitor.GetNamespace()).
		Get(ctx, monitor.GetName(), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get %s: %w", kObj, err)
	}

	resource.Status.Set(resource, metav1.Condition{
		Type:    v1alpha1.ConditionType[v1alpha1.ConditionTypeProcessed],
		Status:  statusBool,
		Message: message,
	})

	resource, err = c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(resource.GetNamespace()).
		UpdateStatus(ctx, resource, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update the status of %s: %w", kObj, err)
	}

	return resource, nil
}

func (c *Controller) emitFailure(ctx context.Context, monitor *v1alpha1.ResourceMetricsMonitor, message string) {
	kObj := klog.KObj(monitor).String()

	resource, err := c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(monitor.GetNamespace()).
		Get(ctx, monitor.GetName(), metav1.GetOptions{})
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to get %s: %w", kObj, err))

		return
	}

	resource.Status.Set(resource, metav1.Condition{
		Type:    v1alpha1.ConditionType[v1alpha1.ConditionTypeFailed],
		Status:  metav1.ConditionTrue,
		Message: message,
	})

	_, err = c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(resource.GetNamespace()).
		UpdateStatus(ctx, resource, metav1.UpdateOptions{})
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to emit failure on %s: %w", kObj, err))
	}
}

func (c *Controller) updateMetadata(ctx context.Context, resource *v1alpha1.ResourceMetricsMonitor) error {
	logger := klog.FromContext(ctx)
	kObj := klog.KObj(resource).String()

	return wait.PollUntilContextTimeout(ctx, time.Second, time.Minute, false, func(pollCtx context.Context) (bool, error) {
		gotResource, err := c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(resource.GetNamespace()).Get(pollCtx, resource.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to get %s: %w", kObj, err)
		}

		resource = gotResource.DeepCopy()

		if resource.Labels == nil {
			resource.Labels = make(map[string]string)
		}

		resource.Labels["app.kubernetes.io/managed-by"] = version.ControllerName.String()
		revisionSHA := revisionSHARegex.FindStringSubmatch(version.Version())

		if len(revisionSHA) > 1 {
			resource.Labels["app.kubernetes.io/version"] = revisionSHA[1]
		} else {
			logger.Error(errors.New("failed to get revision SHA, continuing anyway"), "cannot set version label")
		}

		resource, err = c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(resource.GetNamespace()).Update(pollCtx, resource, metav1.UpdateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to update %s: %w", kObj, err)
		}

		return true, nil
	})
}

// cardinalityAggregation holds aggregated cardinality data from stores.
type cardinalityAggregation struct {
	totalCardinality int64
	perStore         map[string]int64
	perStoreLimit    map[string]int64
	perFamily        map[string]int64
	perFamilyLimit   map[string]int64
	cutoffFamilies   []string
	violations       []ThresholdViolation
}

// aggregateStoreCardinality aggregates cardinality data from all stores.
func (c *Controller) aggregateStoreCardinality(stores []*StoreType, resource *v1alpha1.ResourceMetricsMonitor) cardinalityAggregation {
	agg := cardinalityAggregation{
		perStore:       make(map[string]int64),
		perStoreLimit:  make(map[string]int64),
		perFamily:      make(map[string]int64),
		perFamilyLimit: make(map[string]int64),
	}

	for _, store := range stores {
		if store.cardinalityTracker == nil {
			continue
		}

		storeID := store.GetStoreIdentifier()
		storeTotal := store.cardinalityTracker.GetStoreTotal()
		agg.perStore[storeID] = storeTotal
		agg.perStoreLimit[storeID] = store.cardinalityTracker.GetStoreThreshold()
		agg.totalCardinality += storeTotal

		familyCards := store.cardinalityTracker.GetAllFamilyCardinalities()
		for family, count := range familyCards {
			agg.perFamily[family] += count
		}

		familyLimits := store.cardinalityTracker.GetAllFamilyThresholds()
		for family, limit := range familyLimits {
			// Use max if family appears in multiple stores (unusual but possible)
			if limit > agg.perFamilyLimit[family] {
				agg.perFamilyLimit[family] = limit
			}
		}

		agg.cutoffFamilies = append(agg.cutoffFamilies, store.cardinalityTracker.GetCutoffFamilies()...)

		violations := store.checkAndApplyThresholds()
		for i := range violations {
			violations[i].StoreName = storeID
			violations[i].RMMName = resource.GetName()
			violations[i].RMMNamespace = resource.GetNamespace()
		}

		agg.violations = append(agg.violations, violations...)
	}

	return agg
}

// hasThresholdExceeded checks if any violation indicates a threshold exceeded (not just warning).
func hasThresholdExceeded(violations []ThresholdViolation) bool {
	for _, v := range violations {
		if v.Severity == SeverityCutoff {
			return true
		}
	}

	return false
}

// updateCardinalityStatus aggregates cardinality from all stores for an RMM and updates status.
func (c *Controller) updateCardinalityStatus(ctx context.Context, resource *v1alpha1.ResourceMetricsMonitor) error {
	logger := klog.FromContext(ctx)
	kObj := klog.KObj(resource).String()

	storesI, ok := c.stores.Load(resource.GetUID())
	if !ok {
		logger.V(2).Info("No stores found for resource", "resource", kObj)

		return nil
	}

	stores, ok := storesI.([]*StoreType)
	if !ok {
		logger.Error(errors.New("failed to cast stores"), "cannot update cardinality status")

		return nil
	}

	agg := c.aggregateStoreCardinality(stores, resource)

	c.globalCardinalityManager.UpdateResource(resource.GetUID(), agg.totalCardinality)

	resourceViolations := c.globalCardinalityManager.CheckThresholds(resource.GetUID(), 0)
	for i := range resourceViolations {
		resourceViolations[i].RMMName = resource.GetName()
		resourceViolations[i].RMMNamespace = resource.GetNamespace()
	}

	agg.violations = append(agg.violations, resourceViolations...)

	c.updateCardinalityMetrics(resource, agg)
	c.recordCardinalityViolations(resource, agg.violations)

	return c.persistCardinalityStatus(ctx, resource, agg)
}

// recordCardinalityViolations increments the cardinality_exceeded_total metric for violations.
func (c *Controller) recordCardinalityViolations(resource *v1alpha1.ResourceMetricsMonitor, violations []ThresholdViolation) {
	for _, v := range violations {
		if v.Severity == SeverityCutoff {
			c.cardinalityExceeded.WithLabelValues(
				resource.GetNamespace(),
				resource.GetName(),
				string(v.Level),
				v.Name,
			).Inc()
		}
	}
}

// persistCardinalityStatus updates the RMM status with cardinality information.
func (c *Controller) persistCardinalityStatus(ctx context.Context, resource *v1alpha1.ResourceMetricsMonitor, agg cardinalityAggregation) error {
	kObj := klog.KObj(resource).String()

	gotResource, err := c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(resource.GetNamespace()).
		Get(ctx, resource.GetName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get %s: %w", kObj, err)
	}

	gotResource.Status.Cardinality = &v1alpha1.CardinalityStatus{
		Total:              agg.totalCardinality,
		PerStore:           agg.perStore,
		PerFamily:          agg.perFamily,
		ThresholdsExceeded: hasThresholdExceeded(agg.violations),
		CutoffFamilies:     agg.cutoffFamilies,
		LastUpdated:        metav1.Now(),
	}

	c.setCardinalityConditions(gotResource, agg.violations)

	_, err = c.rsmClientset.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(gotResource.GetNamespace()).
		UpdateStatus(ctx, gotResource, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update cardinality status of %s: %w", kObj, err)
	}

	return nil
}

// updateCardinalityMetrics updates the Prometheus cardinality metrics.
func (c *Controller) updateCardinalityMetrics(resource *v1alpha1.ResourceMetricsMonitor, agg cardinalityAggregation) {
	namespace := resource.GetNamespace()
	name := resource.GetName()

	for storeID, count := range agg.perStore {
		c.storeCardinality.WithLabelValues(namespace, name, storeID).Set(float64(count))
	}

	for storeID, limit := range agg.perStoreLimit {
		c.storeCardinalityLimit.WithLabelValues(namespace, name, storeID).Set(float64(limit))
	}

	for family, count := range agg.perFamily {
		// We don't have per-store breakdown for family metrics here, so use empty store label.
		c.familyCardinality.WithLabelValues(namespace, name, "", family).Set(float64(count))
	}

	for family, limit := range agg.perFamilyLimit {
		c.familyCardinalityLimit.WithLabelValues(namespace, name, "", family).Set(float64(limit))
	}

	c.resourceCardinality.WithLabelValues(namespace, name).Set(float64(agg.totalCardinality))

	// Resource limit comes from config or default
	resourceLimit := c.globalCardinalityManager.GetResourceDefaultThreshold()
	c.resourceCardinalityLimit.WithLabelValues(namespace, name).Set(float64(resourceLimit))

	c.globalCardinality.Set(float64(c.globalCardinalityManager.GetGlobalTotal()))
	c.globalCardinalityLimit.Set(float64(c.globalCardinalityManager.GetGlobalThreshold()))
}

// setCardinalityConditions sets the appropriate cardinality conditions on the resource.
func (c *Controller) setCardinalityConditions(resource *v1alpha1.ResourceMetricsMonitor, violations []ThresholdViolation) {
	hasWarning := false
	hasCutoff := false

	for _, v := range violations {
		switch v.Severity {
		case SeverityWarning:
			hasWarning = true
		case SeverityCutoff:
			hasCutoff = true
		}
	}

	// Set CardinalityCutoff condition. When not cut off but a warning is active,
	// the reason reflects that we are in the warning zone even though generation
	// has not been halted.
	if hasCutoff {
		resource.Status.Set(resource, metav1.Condition{
			Type:   v1alpha1.ConditionType[v1alpha1.ConditionTypeCardinalityCutoff],
			Status: metav1.ConditionTrue,
		})
	} else {
		reason := v1alpha1.ConditionReasonFalse[v1alpha1.ConditionTypeCardinalityCutoff]
		if hasWarning {
			reason = v1alpha1.ConditionReasonTrue[v1alpha1.ConditionTypeCardinalityWarning]
		}

		resource.Status.Set(resource, metav1.Condition{
			Type:   v1alpha1.ConditionType[v1alpha1.ConditionTypeCardinalityCutoff],
			Status: metav1.ConditionFalse,
			Reason: reason,
		})
	}

	// Set CardinalityWarning condition. Warning persists even when cutoff is active —
	// exceeding the threshold implies being above the warning level too.
	if hasWarning || hasCutoff {
		resource.Status.Set(resource, metav1.Condition{
			Type:   v1alpha1.ConditionType[v1alpha1.ConditionTypeCardinalityWarning],
			Status: metav1.ConditionTrue,
		})
	} else {
		resource.Status.Set(resource, metav1.Condition{
			Type:   v1alpha1.ConditionType[v1alpha1.ConditionTypeCardinalityWarning],
			Status: metav1.ConditionFalse,
		})
	}
}

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
	stderrors "errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kubernetes-sigs/resource-state-metrics/internal/version"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	clientset "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/clientset/versioned"
	rsmscheme "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/clientset/versioned/scheme"
	informers "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/informers/externalversions"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/options"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	rateLimiterBaseDelay = 5 * time.Millisecond
	rateLimiterMaxDelay  = 5 * time.Minute
	rateLimiterQPS       = 50
	rateLimiterBurst     = 300
)

// schemeOnce ensures scheme registration happens only once (thread-safe for parallel tests).
var schemeOnce sync.Once

// workItem is a single workqueue entry. uid is captured at enqueue time,
// rather than re-derived when the item is processed, because a deleteEvent's
// underlying resource is already gone from both the API server and the
// informer cache by the time syncHandler runs, and c.stores is keyed by UID.
type workItem struct {
	key   string
	event string
	uid   types.UID
}

// NOTE: When adding new metrics, consider revisiting the alerting mixins.
type metrics struct {
	requestDurationVec       *prometheus.HistogramVec
	resourcesMonitored       *prometheus.GaugeVec
	eventsProcessed          *prometheus.CounterVec
	celEvaluations           *prometheus.CounterVec
	familyCardinality        *prometheus.GaugeVec
	familyCardinalityLimit   *prometheus.GaugeVec
	storeCardinality         *prometheus.GaugeVec
	storeCardinalityLimit    *prometheus.GaugeVec
	resourceCardinality      *prometheus.GaugeVec
	resourceCardinalityLimit *prometheus.GaugeVec
	globalCardinality        prometheus.Gauge
	globalCardinalityLimit   prometheus.Gauge
	cardinalityExceeded      *prometheus.CounterVec
	duplicateStores          *prometheus.GaugeVec
	duplicateFamilies        *prometheus.GaugeVec
}

// Controller is the controller implementation for managed resources.
type Controller struct {
	kubeclientset            kubernetes.Interface
	rsmClientset             clientset.Interface
	dynamicClientset         dynamic.Interface
	resourceDiscovery        ResourceDiscovery
	rsmInformerFactory       informers.SharedInformerFactory
	workqueue                workqueue.TypedRateLimitingInterface[workItem]
	recorder                 record.EventRecorder
	stores                   sync.Map
	options                  *options.Options
	globalCardinalityManager *GlobalCardinalityManager

	metrics
}

// NewController returns a new controller instance.
func NewController(
	ctx context.Context,
	options *options.Options,
	kubeClientset kubernetes.Interface,
	rsmClientset clientset.Interface,
	dynamicClientset dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
) *Controller {
	logger := klog.FromContext(ctx)

	schemeOnce.Do(func() {
		utilruntime.Must(rsmscheme.AddToScheme(scheme.Scheme))
	})

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: kubeClientset.CoreV1().Events(metav1.NamespaceNone),
	})

	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: version.ControllerName.String()})

	ratelimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[workItem](rateLimiterBaseDelay, rateLimiterMaxDelay),
		&workqueue.TypedBucketRateLimiter[workItem]{Limiter: rate.NewLimiter(rate.Limit(rateLimiterQPS), rateLimiterBurst)},
	)

	controller := &Controller{
		kubeclientset:      kubeClientset,
		rsmClientset:       rsmClientset,
		dynamicClientset:   dynamicClientset,
		resourceDiscovery:  NewResourceDiscovery(discoveryClient, logger),
		rsmInformerFactory: informers.NewSharedInformerFactory(rsmClientset, 0),
		workqueue:          workqueue.NewTypedRateLimitingQueue[workItem](ratelimiter),
		recorder:           recorder,
		options:            options,
		globalCardinalityManager: NewGlobalCardinalityManager(
			*options.GlobalCardinalityLimit,
			*options.ResourceCardinalityDefault,
			*options.CardinalityWarningRatio,
		),
	}

	controller.registerEventHandlers(logger)

	return controller
}

// Run starts the controller and blocks until the context is cancelled.
//
//nolint:funlen // metric registration + server setup is a single cohesive startup sequence
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.V(1).Info("Starting controller")
	logger.V(4).Info("Waiting for informer caches to sync")

	c.rsmInformerFactory.Start(ctx.Done())

	if ok := cache.WaitForCacheSync(ctx.Done(), c.rsmInformerFactory.ResourceStateMetrics().V1alpha1().ResourceMetricsMonitors().Informer().HasSynced); !ok {
		return stderrors.New("failed to wait for caches to sync")
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		versioncollector.NewCollector(version.ControllerName.ToSnakeCase()),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{Namespace: version.ControllerName.ToSnakeCase(), ReportErrors: true}),
	)

	namespace := version.ControllerName.ToSnakeCase()
	c.requestDurationVec = promauto.With(registry).NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "A histogram of requests for the main server's metrics endpoint.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "code"})

	c.resourcesMonitored = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "resources_monitored_info",
		Help:      "Information about ResourceMetricsMonitor resources currently being monitored.",
	}, []string{"namespace", "name"})

	c.eventsProcessed = promauto.With(registry).NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "events_processed_total",
		Help:      "Total number of events processed by type and status.",
	}, []string{"namespace", "name", "event_type", "status"})

	c.celEvaluations = promauto.With(registry).NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cel_evaluations_total",
		Help:      "Total number of CEL expression evaluations by result.",
	}, []string{"namespace", "name", "family", "result"})

	c.familyCardinality = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "family_cardinality",
		Help:      "Current cardinality (number of time series) per metric family.",
	}, []string{"namespace", "name", "store", "family"})

	c.familyCardinalityLimit = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "family_cardinality_limit",
		Help:      "Configured cardinality limit per metric family. 0 means unlimited.",
	}, []string{"namespace", "name", "store", "family"})

	c.storeCardinality = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "store_cardinality",
		Help:      "Current cardinality (number of time series) per store.",
	}, []string{"namespace", "name", "store"})

	c.storeCardinalityLimit = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "store_cardinality_limit",
		Help:      "Configured cardinality limit per store. 0 means unlimited.",
	}, []string{"namespace", "name", "store"})

	c.resourceCardinality = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "resource_cardinality",
		Help:      "Current cardinality (number of time series) per ResourceMetricsMonitor.",
	}, []string{"namespace", "name"})

	c.resourceCardinalityLimit = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "resource_cardinality_limit",
		Help:      "Configured cardinality limit per ResourceMetricsMonitor. 0 means unlimited.",
	}, []string{"namespace", "name"})

	c.globalCardinality = promauto.With(registry).NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "global_cardinality",
		Help:      "Current total cardinality (number of time series) across all ResourceMetricsMonitors.",
	})

	c.globalCardinalityLimit = promauto.With(registry).NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "global_cardinality_limit",
		Help:      "Configured global cardinality limit across all ResourceMetricsMonitors. 0 means unlimited.",
	})

	c.cardinalityExceeded = promauto.With(registry).NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cardinality_exceeded_total",
		Help:      "Total number of times cardinality thresholds were exceeded.",
	}, []string{"namespace", "name", "level", "entity"})

	c.duplicateStores = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "duplicate_stores",
		Help:      "Current number of duplicate stores detected (same GVK from wildcard expansion or explicit config). Resets to 0 when config is fixed.",
	}, []string{"namespace", "name", "store"})

	c.duplicateFamilies = promauto.With(registry).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "duplicate_families",
		Help:      "Current number of stores sharing the same family name. Resets to 0 when config is fixed.",
	}, []string{"namespace", "name", "family"})

	selfAddr := net.JoinHostPort(*c.options.SelfHost, strconv.Itoa(*c.options.SelfPort))
	mainAddr := net.JoinHostPort(*c.options.MainHost, strconv.Itoa(*c.options.MainPort))

	self := newSelfServer(selfAddr).build(ctx, c.kubeclientset, registry)
	main := newMainServer(mainAddr, *c.options.Kubeconfig, &c.stores, c.requestDurationVec).build(ctx, c.kubeclientset, registry)

	logger.V(1).Info("Starting workers")

	for range workers {
		go wait.UntilWithContext(ctx, func(ctx context.Context) {
			for c.processNextWorkItem(ctx) {
			}
		}, time.Second)
	}

	go func() {
		logger.V(1).Info("Starting telemetry server on", "address", selfAddr)

		if err := self.ListenAndServe(); err != nil {
			logger.Error(err, "stopping telemetry server")
		}
	}()
	go func() {
		logger.V(1).Info("Starting main server on", "address", mainAddr)

		if err := main.ListenAndServe(); err != nil {
			logger.Error(err, "stopping main server")
		}
	}()

	<-ctx.Done()
	logger.V(1).Info("Shutting down servers")

	if err := self.Shutdown(ctx); err != nil {
		logger.Error(err, "error shutting down telemetry server")
	}

	if err := main.Shutdown(ctx); err != nil {
		logger.Error(err, "error shutting down main server")
	}

	return nil
}

func (c *Controller) registerEventHandlers(logger klog.Logger) {
	_, err := c.rsmInformerFactory.ResourceStateMetrics().V1alpha1().ResourceMetricsMonitors().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueue(obj, addEvent) },
		UpdateFunc: c.updateHandler(logger),
		DeleteFunc: func(obj interface{}) { c.enqueue(obj, deleteEvent) },
	})
	if err != nil {
		logger.Error(err, "error setting up event handlers")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
}

func (c *Controller) updateHandler(logger klog.Logger) func(interface{}, interface{}) {
	return func(oldI, newI interface{}) {
		oldResource, ok := oldI.(*v1alpha1.ResourceMetricsMonitor)
		if !ok {
			logger.Error(stderrors.New("failed to cast old object to ResourceMetricsMonitor"), "cannot handle update event")

			return
		}

		newResource, ok := newI.(*v1alpha1.ResourceMetricsMonitor)
		if !ok {
			logger.Error(stderrors.New("failed to cast new object to ResourceMetricsMonitor"), "cannot handle update event")

			return
		}

		if oldResource.ResourceVersion == newResource.ResourceVersion || reflect.DeepEqual(oldResource.Spec, newResource.Spec) {
			logger.V(10).Info("Skipping event", "[-old +new]", cmp.Diff(oldResource, newResource))

			return
		}

		logger.V(4).Info("Update event", "[-old +new]", cmp.Diff(oldResource.Spec.Configuration, newResource.Spec.Configuration))
		c.enqueue(newI, updateEvent)
	}
}

func (c *Controller) enqueue(obj interface{}, event eventType) {
	object, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type %T", obj))

			return
		}

		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type %T", tombstone.Obj))

			return
		}
	}

	key, err := cache.MetaNamespaceKeyFunc(object)
	if err != nil {
		utilruntime.HandleError(err)

		return
	}

	c.workqueue.Add(workItem{key: key, event: event.String(), uid: object.GetUID()})
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	logger := klog.FromContext(ctx)

	item, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}

	err := func(item workItem) error {
		defer c.workqueue.Done(item)

		if err := c.syncHandler(ctx, item.key, item.event, item.uid); err != nil {
			c.workqueue.AddRateLimited(item)

			return fmt.Errorf("error syncing '%s': %s, requeuing", item.key, err.Error())
		}

		c.workqueue.Forget(item)
		logger.V(4).Info("Synced", "key", item.key)

		return nil
	}(item)
	if err != nil {
		logger.Error(err, "error processing item")

		return true
	}

	return true
}

func (c *Controller) syncHandler(ctx context.Context, key string, event string, uid types.UID) error {
	logger := klog.FromContext(ctx)
	logger.V(4).Info("Syncing", "key", key, "event", event)

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		logger.Error(err, "invalid resource key", "key", key)

		return nil
	}

	resource, err := c.rsmInformerFactory.ResourceStateMetrics().V1alpha1().ResourceMetricsMonitors().Lister().ResourceMetricsMonitors(namespace).Get(name)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("error getting ResourceMetricsMonitor %q: %w", klog.KRef(namespace, name), err)
	}

	if errors.IsNotFound(err) {
		// The resource is already gone from the API server and the informer
		// cache by the time delete events are processed, so its identity has
		// to be reconstructed from what enqueue captured beforehand.
		resource = &v1alpha1.ResourceMetricsMonitor{}
		resource.SetNamespace(namespace)
		resource.SetName(name)
		resource.SetUID(uid)
	}

	return c.handleObject(ctx, resource, event)
}

func (c *Controller) handleObject(ctx context.Context, objectI interface{}, event string) error {
	logger := klog.FromContext(ctx)
	if objectI == nil {
		logger.Error(stderrors.New("received nil object for handling, skipping"), "error handling object")

		return nil
	}

	var object metav1.Object

	var ok bool
	if object, ok = objectI.(metav1.Object); !ok {
		tombstone, ok := objectI.(cache.DeletedFinalStateUnknown)
		if !ok {
			logger.Error(stderrors.New("error decoding object, invalid type"), "error handling object")

			return nil
		}

		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			logger.Error(stderrors.New("error decoding object tombstone, invalid type"), "error handling object")

			return nil
		}

		logger.V(1).Info("Recovered", "key", klog.KObj(object))
	}

	logger = klog.LoggerWithValues(klog.FromContext(ctx), "key", klog.KObj(object), "event", event)
	logger.V(1).Info("Processing object")

	switch o := object.(type) {
	case *v1alpha1.ResourceMetricsMonitor:
		return c.handleEvent(ctx, &c.stores, event, o)
	default:
		logger.Error(stderrors.New("unknown object type"), "cannot handle object")

		return nil
	}
}

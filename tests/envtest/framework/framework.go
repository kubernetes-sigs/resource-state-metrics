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

package framework

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/internal"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	rsmclientset "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/clientset/versioned"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/options"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	memorycache "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

const (
	pollInterval           = 100 * time.Millisecond
	controllerReadyTimeout = 30 * time.Second
)

// Framework provides utilities for envtest-based integration tests backed by
// real Kubernetes clients.
type Framework struct {
	Options   *options.Options
	RSMClient rsmclientset.Interface

	kubeClient      kubernetes.Interface
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	restMapper      meta.RESTMapper
	controller      *internal.Controller
	controllerDone  chan struct{}
}

// NewForConfig creates a new Framework backed by real clients from the given
// rest.Config. This is intended for envtest-style tests where a real
// kube-apiserver is available.
func NewForConfig(_ context.Context, cfg *rest.Config) (*Framework, error) {
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	rsmClient, err := rsmclientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create RSM client: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memorycache.NewMemCacheClient(discoClient))

	// The test framework needs production defaults, not process CLI parsing.
	// Read uses the global flag set and may conflict with controller-runtime's test flags.
	opts := options.NewOptions(klog.Background())

	return &Framework{
		Options:         opts,
		RSMClient:       rsmClient,
		kubeClient:      kubeClient,
		dynamicClient:   dynClient,
		discoveryClient: discoClient,
		restMapper:      mapper,
	}, nil
}

// Start configures workers and ports, creates the controller, launches it in a
// background goroutine, and waits for the main server's /healthz endpoint to
// return 200 OK before returning.
func (f *Framework) Start(ctx context.Context, workers int) error {
	f.Options.Workers = &workers

	listeners, ports, err := testutil.ListenOnFreePorts(ctx, 2)
	if err != nil {
		return fmt.Errorf("failed to allocate metrics ports: %w", err)
	}

	f.Options.MainPort = &ports[0]
	f.Options.SelfPort = &ports[1]

	f.controller = internal.NewController(ctx, f.Options, f.kubeClient, f.RSMClient, f.dynamicClient, f.discoveryClient)

	f.controllerDone = make(chan struct{})

	go func() {
		defer close(f.controllerDone)

		if err := f.controller.RunWithListeners(ctx, *f.Options.Workers, listeners[1], listeners[0]); err != nil {
			klog.FromContext(ctx).Error(err, "controller exited with error")
		}
	}()

	healthzURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", *f.Options.MainPort)
	if err := testutil.WaitForHTTPOK(ctx, healthzURL, controllerReadyTimeout, pollInterval); err != nil {
		return fmt.Errorf("controller failed to become ready: %w", err)
	}

	return nil
}

// WaitForControllerExit blocks until the controller goroutine started by Start
// has returned (the context passed to Start must be cancelled first), or the
// timeout expires. It is a no-op if Start was never called.
func (f *Framework) WaitForControllerExit(timeout time.Duration) error {
	if f.controllerDone == nil {
		return nil
	}

	select {
	case <-f.controllerDone:
		return nil
	case <-time.After(timeout):
		return errors.New("timed out waiting for the controller to exit")
	}
}

// ApplyCRFromYAML reads a YAML file, unmarshals it to an unstructured object,
// and applies it with ApplyUnstructured. See ApplyUnstructured for the
// semantics of the returned cleanup function.
func (f *Framework) ApplyCRFromYAML(ctx context.Context, path string) (func(context.Context) error, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file %s: %w", path, err)
	}

	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return f.ApplyUnstructured(ctx, obj)
}

// ApplyUnstructured resolves the GVR of desired via the REST mapper and creates
// it using the dynamic client. If the object already exists (e.g. when running
// against an existing cluster), it is updated in place.
//
// The returned cleanup function undoes the apply: it deletes an object that
// this call created, and restores a pre-existing object to the state it had
// before the update. It is a no-op if the object is gone, and it returns an
// error without touching the object if it was replaced in the meantime.
func (f *Framework) ApplyUnstructured(
	ctx context.Context,
	desired *unstructured.Unstructured,
) (func(context.Context) error, error) {
	obj := desired.DeepCopy()
	gvk := obj.GroupVersionKind()

	mapping, err := f.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get REST mapping for %s: %w", gvk, err)
	}

	client := f.dynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace())
	ref := fmt.Sprintf("%s %s/%s", gvk.Kind, obj.GetNamespace(), obj.GetName())

	created, err := client.Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return undoApply(client, ref, created, nil), nil
	}

	if !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create %s: %w", ref, err)
	}

	previous, err := client.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get existing %s: %w", ref, err)
	}

	obj.SetResourceVersion(previous.GetResourceVersion())

	updated, err := client.Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update existing %s: %w", ref, err)
	}

	return undoApply(client, ref, updated, previous), nil
}

// undoApply returns the cleanup function for an applied object. A nil previous
// means the object was created by the apply and is deleted; otherwise the
// object is restored to previous. Either way the object is only touched if it
// still has the UID it had when it was applied.
func undoApply(
	client dynamic.ResourceInterface,
	ref string,
	applied, previous *unstructured.Unstructured,
) func(context.Context) error {
	name := applied.GetName()
	uid := applied.GetUID()

	return func(ctx context.Context) error {
		current, err := client.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("failed to get %s for cleanup: %w", ref, err)
		}

		if current.GetUID() != uid {
			return fmt.Errorf("%s was replaced during the test; refusing to clean it up", ref)
		}

		if previous == nil {
			deleteOptions := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}
			if err := client.Delete(ctx, name, deleteOptions); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete %s: %w", ref, err)
			}

			return nil
		}

		restored := previous.DeepCopy()
		restored.SetResourceVersion(current.GetResourceVersion())

		if _, err := client.Update(ctx, restored, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to restore %s: %w", ref, err)
		}

		return nil
	}
}

// CompareMainMetrics scrapes the main metrics endpoint and compares the result
// against expectedMetricLines, filtering to only the metric families declared
// in the expected text (identified by "# TYPE" lines). Returns nil on match.
func (f *Framework) CompareMainMetrics(expectedMetricLines []string) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", *f.Options.MainPort)

	return testutil.CompareMetrics(url, expectedMetricLines)
}

// WaitForRMMProcessed polls the RSM client until the named ResourceMetricsMonitor
// has a Processed status condition set to True, or the timeout expires. The
// controller first writes Processed=False while an event is in flight, so the
// mere presence of the condition is not enough.
func (f *Framework) WaitForRMMProcessed(ctx context.Context, namespace, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for RMM %s/%s to be processed: %w", namespace, name, ctx.Err())
		case <-ticker.C:
			rmm, err := f.RSMClient.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				continue
			}

			for _, cond := range rmm.Status.Conditions {
				if cond.Type == v1alpha1.ConditionType[v1alpha1.ConditionTypeProcessed] && cond.Status == metav1.ConditionTrue {
					return nil
				}
			}
		}
	}
}

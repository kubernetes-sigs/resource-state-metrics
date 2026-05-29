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
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/internal"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	rsmclientset "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/clientset/versioned"
	rsmfake "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/clientset/versioned/fake"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/options"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

const (
	gvkIndexName = "gvk"

	ShortTimeInterval = 100 * time.Millisecond
	LongTimeInterval  = time.Second
)

var (
	rmmGVR = schema.GroupVersionResource{
		Group:    v1alpha1.SchemeGroupVersion.Group,
		Version:  v1alpha1.SchemeGroupVersion.Version,
		Resource: "resourcemetricsmonitors",
	}
)

// Framework provides utilities for e2e testing with mock clientsets.
type Framework struct {
	Options   *options.Options
	RSMClient rsmclientset.Interface

	apiExtensionsClient apiextensionsclientset.Interface
	controller          *internal.Controller
	crdInformer         cache.SharedIndexInformer
	crdInformerFactory  apiextensionsinformers.SharedInformerFactory
	dynamicClient       *dynamicfake.FakeDynamicClient
	kubeClient          kubernetes.Interface
	scheme              *runtime.Scheme
	discoveryResources  []*metav1.APIResourceList
}

// NewInforming creates a new test framework with mock clientsets, and starts the CRD informer to keep it populated for test operations.
// Optional initial RMMs can be provided to pre-populate the fake RSM client before the controller starts.
func NewInforming(ctx context.Context, initialObjects ...runtime.Object) *Framework {
	apiExtensionsClient := apiextensionsfake.NewSimpleClientset()
	crdInformerFactory := apiextensionsinformers.NewSharedInformerFactory(apiExtensionsClient, 0)
	crdInformer := crdInformerFactory.Apiextensions().V1().CustomResourceDefinitions().Informer()
	_ = crdInformer.AddIndexers(cache.Indexers{
		gvkIndexName: func(obj any) ([]string, error) {
			crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
			if !ok {
				return nil, errors.New("object is not a CRD")
			}

			var keys []string

			for _, version := range crd.Spec.Versions {
				gvk := schema.GroupVersionKind{
					Group:   crd.Spec.Group,
					Version: version.Name,
					Kind:    crd.Spec.Names.Kind,
				}
				keys = append(keys, gvk.String())
			}

			return keys, nil
		},
	})

	f := &Framework{
		kubeClient:          kubefake.NewClientset(),
		RSMClient:           rsmfake.NewSimpleClientset(initialObjects...),
		apiExtensionsClient: apiExtensionsClient,
		scheme:              runtime.NewScheme(), // use f.AddToScheme to inject types into the scheme
		crdInformer:         crdInformer,
		crdInformerFactory:  crdInformerFactory,
	}

	crdInformerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), crdInformer.HasSynced)

	return f
}

// AddToScheme adds types to the framework's scheme. Panics if any adder returns an error.
func (f *Framework) AddToScheme(adder func(*runtime.Scheme)) *runtime.Scheme {
	adder(f.scheme)

	return f.scheme
}

// WithDynamicClient initializes the dynamic client with the provided custom
// GVR to ListKind mapping. Panics if the scheme is not initialized or has no
// known types, as the dynamic client relies on the scheme for object mapping.
// The caller must ensure that the mapping is consistent with the types added
// to the scheme via AddToScheme().
func (f *Framework) WithDynamicClient(injectedCustomGVRToListKind map[schema.GroupVersionResource]string) {
	if f.scheme == nil {
		panic("scheme is not initialized; call AddToScheme() to initialize the scheme before setting up the dynamic client")
	}

	f.dynamicClient = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(f.scheme, injectedCustomGVRToListKind)
}

// WithDiscoveryResources registers additional API resources for the fake discovery client.
// This allows injection of explicit resources beyond what is automatically inferred from
// CRDs registered via CreateCRDFromYAML. Use this for core resources (pods, services, etc.)
// or other non-CRD resources that need to be discoverable during wildcard expansion tests.
func (f *Framework) WithDiscoveryResources(resources []*metav1.APIResourceList) {
	f.discoveryResources = resources
}

// Start starts the RSM controller with the mock clients.
// NOTE: This spawns a new Controller instance for each call to a newly
// instantiated Framework. It is thus advised to review added code for
// concurrent access, and enforced.
func (f *Framework) Start(ctx context.Context, workers int) error {
	switch {
	case f.dynamicClient == nil:
		panic("dynamic client is not initialized; call WithDynamicClient() to initialize it before starting the controller")
	case len(f.scheme.AllKnownTypes()) == 0:
		panic("scheme has no known types; call AddToScheme() to add types to the scheme before starting the controller")
	}

	// Check if controller is already running
	if f.controller != nil {
		return nil
	}

	f.Options = &options.Options{Workers: &workers}
	f.Options.Read()

	// Allocate free ports dynamically to avoid conflicts between tests
	mainPort, err := getFreePort(ctx)
	if err != nil {
		return fmt.Errorf("failed to allocate main port: %w", err)
	}

	f.Options.MainPort = &mainPort

	selfPort, err := getFreePort(ctx)
	if err != nil {
		return fmt.Errorf("failed to allocate self port: %w", err)
	}

	f.Options.SelfPort = &selfPort

	// Build discovery client from registered CRDs and any explicit resources
	fakeKubeClient, ok := f.kubeClient.(*kubefake.Clientset)
	if !ok {
		return errors.New("failed to cast kube client to fake clientset")
	}

	fakeDiscovery, ok := fakeKubeClient.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		return errors.New("failed to cast discovery client to fake discovery")
	}

	// Combine explicit resources with CRD-derived resources
	allResources := f.discoveryResources
	crdResources := f.buildDiscoveryResourcesFromCRDs()
	allResources = append(allResources, crdResources...)
	fakeDiscovery.Resources = allResources

	f.controller = internal.NewController(ctx, f.Options, f.kubeClient, f.RSMClient, f.dynamicClient, fakeDiscovery)

	// Start controller in background
	go func() {
		if err := f.controller.Run(ctx, *f.Options.Workers); err != nil {
			klog.FromContext(ctx).Error(err, "controller failed to start")
		}
	}()

	if err := f.waitForControllerReady(ctx); err != nil {
		return fmt.Errorf("controller failed to become ready: %w", err)
	}

	return nil
}

// GetGoldenRuleFiles returns all golden rule file paths for the specified resolver types.
func GetGoldenRuleFiles(resolverType []v1alpha1.ResolverType) []string {
	var files []string //nolint:prealloc

	for _, resolverType := range resolverType {
		goldenDir := filepath.Join("golden", string(resolverType))
		if _, err := os.Stat(goldenDir); os.IsNotExist(err) {
			panic(fmt.Sprintf("golden rules directory does not exist for resolver type %s: expected at %s", resolverType, goldenDir))
		}

		matches, _ := filepath.Glob(filepath.Join(goldenDir, "*.yaml"))
		files = append(files, matches...)
	}

	return files
}

// GoldenRule defines the structure of a golden rule for testing metric generation.
// Every field is required; no omitempty allowed, to ensure the test is fully specified.
type GoldenRule struct {
	Name        string                                 `yaml:"name"`
	Description string                                 `yaml:"description"`
	In          *unstructured.Unstructured             `yaml:"in"` // In is resource-agnostic to accommodate for any future resources introduced in RSM.
	Metrics     []string                               `yaml:"metrics"`
	Status      *v1alpha1.ResourceMetricsMonitorStatus `yaml:"status"`
}

// GoldenRuleFromYAML loads a golden rule from a YAML file.
func GoldenRuleFromYAML(_ context.Context, path string) (*GoldenRule, error) {
	data, err := os.ReadFile(ensureSafePath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file %s: %w", path, err)
	}

	goldenRule := &GoldenRule{}
	if err := yaml.Unmarshal(data, goldenRule); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return goldenRule, nil
}

// ApplyCRFromYAML applies a custom resource from a YAML file.
func (f *Framework) ApplyCRFromYAML(ctx context.Context, path string) (*unstructured.Unstructured, error) {
	data, err := os.ReadFile(ensureSafePath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file %s: %w", path, err)
	}

	cr := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, cr); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return f.ApplyCRUnstructured(ctx, cr)
}

// ApplyCRUnstructured applies a custom resource from an unstructured object.
func (f *Framework) ApplyCRUnstructured(ctx context.Context, customresource *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	// Assign a UID if absent, mirroring what the real API server does.
	// Without this, objects keyed on UID="" collide in the metrics store.
	if customresource.GetUID() == "" {
		customresource.SetUID(uuid.NewUUID())
	}

	gvk := customresource.GroupVersionKind()

	resource, err := f.GetResourcePluralNameForGVK(gvk)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource for %s: %w", gvk, err)
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: resource,
	}

	resourceClient := f.dynamicClient.Resource(gvr).Namespace(customresource.GetNamespace())

	created, err := resourceClient.Create(ctx, customresource, metav1.CreateOptions{})
	if err == nil {
		return created, nil
	}

	if !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create CR %s/%s: %w", customresource.GetNamespace(), customresource.GetName(), err)
	}

	existing, err := resourceClient.Get(ctx, customresource.GetName(), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get existing CR %s/%s: %w", customresource.GetNamespace(), customresource.GetName(), err)
	}

	customresource.SetResourceVersion(existing.GetResourceVersion())

	updated, err := resourceClient.Update(ctx, customresource, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update CR %s/%s: %w", customresource.GetNamespace(), customresource.GetName(), err)
	}

	return updated, nil
}

// GetCRUnstructured retrieves a custom resource as an unstructured object.
func (f *Framework) GetCRUnstructured(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	resource, err := f.GetResourcePluralNameForGVK(gvk)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource for %s: %w", gvk, err)
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: resource,
	}

	return f.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// ListCRsUnstructured lists custom resources as unstructured objects.
func (f *Framework) ListCRsUnstructured(ctx context.Context, gvk schema.GroupVersionKind, namespace string) (*unstructured.UnstructuredList, error) {
	resource, err := f.GetResourcePluralNameForGVK(gvk)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource for %s: %w", gvk, err)
	}

	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: resource,
	}

	return f.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
}

// DeleteCR deletes a custom resource.
func (f *Framework) DeleteCR(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) error {
	err := f.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete CR %s/%s: %w", namespace, name, err)
	}

	return nil
}

// CreateCRDFromYAML creates a CRD from a YAML file and waits for it to be indexed.
func (f *Framework) CreateCRDFromYAML(ctx context.Context, path string) (*apiextensionsv1.CustomResourceDefinition, error) {
	data, err := os.ReadFile(ensureSafePath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file %s: %w", path, err)
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(data, crd); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	created, err := f.apiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := f.waitForCRDIndexed(created); err != nil {
		return nil, fmt.Errorf("CRD created but failed to index: %w", err)
	}

	return created, nil
}

// GetIndexedCRDs returns all CRDs currently indexed by the CRD informer.
func (f *Framework) GetIndexedCRDs() []*apiextensionsv1.CustomResourceDefinition {
	var crds []*apiextensionsv1.CustomResourceDefinition

	for _, obj := range f.crdInformer.GetIndexer().List() {
		if crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
			crds = append(crds, crd)
		}
	}

	return crds
}

// GetResourcePluralNameForGVK returns the plural resource name for a given GVK by querying the CRD informer index.
func (f *Framework) GetResourcePluralNameForGVK(gvk schema.GroupVersionKind) (string, error) {
	objs, err := f.crdInformer.GetIndexer().ByIndex(gvkIndexName, gvk.String())
	if err != nil {
		return "", fmt.Errorf("failed to query CRD index for %s: %w", gvk.String(), err)
	}

	if len(objs) == 0 {
		return "", fmt.Errorf("no CRD found for %s", gvk.String())
	}

	crd, ok := objs[0].(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return "", fmt.Errorf("unexpected type in CRD index for %s: %T", gvk.String(), objs[0])
	}

	return crd.Spec.Names.Plural, nil
}

// ToUnstructured converts a runtime.Object to an unstructured.Unstructured.
func (f *Framework) ToUnstructured(o runtime.Object) (*unstructured.Unstructured, error) {
	stringToInterfaceMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to unstructured: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{Object: stringToInterfaceMap}
	unstructuredObj.SetGroupVersionKind(o.GetObjectKind().GroupVersionKind())

	return unstructuredObj, nil
}

// FromUnstructured converts an unstructured.Unstructured back to a runtime.Object (populates the supplied object).
func (f *Framework) FromUnstructured(u *unstructured.Unstructured, o runtime.Object) error {
	return runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, o)
}

// FetchTelemetryMetrics fetches metrics from the telemetry (self) server endpoint.
func (f *Framework) FetchTelemetryMetrics(ctx context.Context) (string, error) {
	return f.fetchFromLocalhost(ctx, *f.Options.SelfPort, "/metrics")
}

// FetchMainMetrics fetches metrics from the main server endpoint.
func (f *Framework) FetchMainMetrics(ctx context.Context) (string, error) {
	return f.fetchFromLocalhost(ctx, *f.Options.MainPort, "/metrics")
}

// buildDiscoveryResourcesFromCRDs builds APIResourceList entries from indexed CRDs.
// This allows wildcard expansion to work with CRDs registered in the framework.
func (f *Framework) buildDiscoveryResourcesFromCRDs() []*metav1.APIResourceList {
	resourcesByGV := make(map[string][]metav1.APIResource)

	for _, obj := range f.crdInformer.GetIndexer().List() {
		crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
		if !ok {
			continue
		}

		for _, version := range crd.Spec.Versions {
			if !version.Served {
				continue
			}

			gv := crd.Spec.Group + "/" + version.Name
			resourcesByGV[gv] = append(resourcesByGV[gv], metav1.APIResource{
				Name:       crd.Spec.Names.Plural,
				Kind:       crd.Spec.Names.Kind,
				Namespaced: crd.Spec.Scope == apiextensionsv1.NamespaceScoped,
				Verbs:      []string{"get", "list", "watch"},
			})
		}
	}

	result := make([]*metav1.APIResourceList, 0, len(resourcesByGV))

	for gv, resources := range resourcesByGV {
		result = append(result, &metav1.APIResourceList{
			GroupVersion: gv,
			APIResources: resources,
		})
	}

	return result
}

// waitForControllerReady waits for the controller to be ready.
func (f *Framework) waitForControllerReady(ctx context.Context) error {
	timeout := time.After(10 * time.Second)

	ticker := time.NewTicker(ShortTimeInterval)
	defer ticker.Stop()

	for {
		port := *f.Options.MainPort

		select {
		case <-timeout:
			return fmt.Errorf("timed out waiting for controller main server port %d to open", port)
		case <-ticker.C:
			dialer := net.Dialer{
				Timeout: ShortTimeInterval,
			}
			addr := fmt.Sprintf("127.0.0.1:%d", port)

			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err == nil {
				_ = conn.Close()

				return nil
			}
		}
	}
}

// waitForCRDIndexed waits for a CRD to appear in the informer index.
func (f *Framework) waitForCRDIndexed(crd *apiextensionsv1.CustomResourceDefinition) error {
	timeout := time.After(LongTimeInterval)

	ticker := time.NewTicker(ShortTimeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out while waiting for CRD (%s) to be indexed", crd.Name)
		case <-ticker.C:
			for _, version := range crd.Spec.Versions {
				gvk := schema.GroupVersionKind{
					Group:   crd.Spec.Group,
					Version: version.Name,
					Kind:    crd.Spec.Names.Kind,
				}

				objs, err := f.crdInformer.GetIndexer().ByIndex(gvkIndexName, gvk.String())
				if err == nil && len(objs) > 0 {
					return nil
				}
			}
		}
	}
}

// fetchFromLocalhost fetches content from a localhost endpoint.
// The port and path are controlled by the framework; only 127.0.0.1 is allowed.
func (f *Framework) fetchFromLocalhost(ctx context.Context, port int, path string) (string, error) {
	// Port is always allocated by getFreePort which only binds to 127.0.0.1.
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	url := "http://" + addr + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: localhostOnlyDialer,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

// localhostOnlyDialer is a DialContext function that only allows connections to 127.0.0.1.
func localhostOnlyDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	if host != "127.0.0.1" {
		return nil, fmt.Errorf("only localhost (127.0.0.1) connections allowed, got %q", host)
	}

	var d net.Dialer

	return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
}

// CRBuilder helps build custom resources.
type CRBuilder struct {
	cr *unstructured.Unstructured
}

// NewCRBuilder returns a builder for constructing unstructured CRs.
func NewCRBuilder(group, version, kind, name, namespace string) *CRBuilder {
	cr := &unstructured.Unstructured{
		Object: make(map[string]any),
	}
	cr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   group,
		Version: version,
		Kind:    kind,
	})
	cr.SetName(name)
	cr.SetNamespace(namespace)

	return &CRBuilder{cr: cr}
}

// WithSpec sets a field in the spec.
// Panics if the field cannot be set.
func (b *CRBuilder) WithSpec(path string, value any) *CRBuilder {
	// Convert int to int64 for JSON compatibility
	switch v := value.(type) {
	case int:
		value = int64(v)
	case int32:
		value = int64(v)
	}

	if err := unstructured.SetNestedField(b.cr.Object, value, "spec", path); err != nil {
		panic(fmt.Sprintf("failed to set spec field %q: %v", path, err))
	}

	return b
}

// WithLabel adds a label.
func (b *CRBuilder) WithLabel(key, value string) *CRBuilder {
	labels := b.cr.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[key] = value
	b.cr.SetLabels(labels)

	return b
}

// WithAnnotation adds an annotation.
func (b *CRBuilder) WithAnnotation(key, value string) *CRBuilder {
	annotations := b.cr.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[key] = value
	b.cr.SetAnnotations(annotations)

	return b
}

// Build returns the constructed unstructured CR.
func (b *CRBuilder) Build() *unstructured.Unstructured {
	return b.cr
}

// ensureSafePath checks if the provided path is within the tests directory to prevent file system access outside of the intended scope.
func ensureSafePath(path string) string {
	cleanedPath := filepath.Clean(path)

	absolutePath, err := filepath.Abs(cleanedPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to get absolute path: %v", err))
	}

	testsDir, err := filepath.Abs("..")
	if err != nil {
		panic(fmt.Sprintf("Failed to get absolute path of tests directory: %v", err))
	}

	if !strings.HasPrefix(absolutePath, testsDir) {
		panic(fmt.Sprintf("Unsafe path detected: %s is outside of the tests directory", absolutePath))
	}

	return absolutePath
}

// getFreePort returns an available port by briefly binding to port 0 (which lets the OS assign a free port).
func getFreePort(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var lc net.ListenConfig

	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to listen on free port: %w", err)
	}

	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("unexpected address type")
	}

	return addr.Port, nil
}

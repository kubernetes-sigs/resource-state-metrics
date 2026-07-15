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
	"path/filepath"
	"strings"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/klog/v2"
)

// WildcardPattern is the pattern used to match all resources.
const WildcardPattern = "*"

// ResourceDiscovery provides methods to discover API resources.
type ResourceDiscovery interface {
	// ExpandWildcards expands a store with wildcard GVK patterns into concrete stores.
	ExpandWildcards(store *v1alpha1.Store) ([]v1alpha1.Store, error)
}

// discoveryClient implements ResourceDiscovery using the Kubernetes discovery API.
type discoveryClient struct {
	client discovery.DiscoveryInterface
	logger klog.Logger
}

// NewResourceDiscovery creates a new ResourceDiscovery using the provided discovery client.
func NewResourceDiscovery(client discovery.DiscoveryInterface, logger klog.Logger) ResourceDiscovery {
	return &discoveryClient{
		client: client,
		logger: logger,
	}
}

// IsWildcard returns true if the pattern contains a wildcard.
func IsWildcard(pattern string) bool {
	return strings.Contains(pattern, WildcardPattern)
}

// HasWildcards returns true if the store has any wildcard patterns.
func HasWildcards(store *v1alpha1.Store) bool {
	return IsWildcard(store.Group) || IsWildcard(store.Version) || IsWildcard(store.Kind) || IsWildcard(store.Resource)
}

// ExpandWildcards expands a store with wildcard GVK patterns into concrete stores.
func (d *discoveryClient) ExpandWildcards(store *v1alpha1.Store) ([]v1alpha1.Store, error) {
	if !HasWildcards(store) {
		return []v1alpha1.Store{*store}, nil
	}

	// Get all API resources from the server
	_, apiResourceLists, err := d.client.ServerGroupsAndResources()
	if err != nil {
		// Discovery may return partial results with errors for some groups
		if apiResourceLists == nil {
			return nil, err
		}

		d.logger.V(2).Info("Partial discovery error (continuing with available resources)", "error", err)
	}

	var expandedStores []v1alpha1.Store

	for _, apiResourceList := range apiResourceLists {
		groupVersion, parseErr := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if parseErr != nil {
			d.logger.V(2).Info("Failed to parse group version", "groupVersion", apiResourceList.GroupVersion, "error", parseErr)

			continue
		}

		if !matchesGroupPattern(store.Group, groupVersion.Group) {
			continue
		}

		if !matchesPattern(store.Version, groupVersion.Version) {
			continue
		}

		for _, apiResource := range apiResourceList.APIResources {
			// Skip subresources (they contain "/")
			if strings.Contains(apiResource.Name, "/") {
				continue
			}

			if !matchesPattern(store.Kind, apiResource.Kind) {
				continue
			}

			if !matchesPattern(store.Resource, apiResource.Name) {
				continue
			}

			// Create expanded store with concrete GVK
			expanded := *store
			expanded.Group = groupVersion.Group
			expanded.Version = groupVersion.Version
			expanded.Kind = apiResource.Kind
			expanded.Resource = apiResource.Name

			// Informers require list+watch. Skip wildcard-matched resources
			// that lack them (e.g. create-only TokenReview and the
			// authorization.k8s.io review resources, whose REST storage
			// supports only create; see k8s.io/kubernetes
			// pkg/registry/{authentication,authorization}/rest) to avoid
			// reflectors that fail with "the server does not allow this
			// method". Explicitly named resources and those with no reported
			// verbs are not filtered.
			if wildcardMatchedResource(store) && len(apiResource.Verbs) > 0 && !supportsListWatch(apiResource.Verbs) {
				d.logger.V(2).Info("Skipping wildcard-matched resource that does not support list+watch",
					"resource", formatStorePattern(&expanded),
					"verbs", apiResource.Verbs)

				continue
			}

			d.logger.V(2).Info("Expanded wildcard store",
				"pattern", formatStorePattern(store),
				"expanded", formatStorePattern(&expanded))

			expandedStores = append(expandedStores, expanded)
		}
	}

	if len(expandedStores) == 0 {
		d.logger.V(1).Info("No resources matched wildcard pattern",
			"group", store.Group,
			"version", store.Version,
			"kind", store.Kind,
			"resource", store.Resource)
	}

	return expandedStores, nil
}

// wildcardMatchedResource reports whether the store matches resources via a
// wildcard Kind or Resource pattern rather than naming one exactly.
func wildcardMatchedResource(store *v1alpha1.Store) bool {
	return IsWildcard(store.Kind) || IsWildcard(store.Resource)
}

// supportsListWatch reports whether the verbs include both list and watch.
func supportsListWatch(verbs []string) bool {
	var hasList, hasWatch bool

	for _, v := range verbs {
		switch v {
		case "list":
			hasList = true
		case "watch":
			hasWatch = true
		}
	}

	return hasList && hasWatch
}

// matchesPattern checks if a value matches a glob-style pattern.
// Supports "*" as a wildcard that matches any sequence of characters.
func matchesPattern(pattern, value string) bool {
	if pattern == WildcardPattern {
		return true
	}

	// Use filepath.Match for glob-style matching
	matched, err := filepath.Match(pattern, value)
	if err != nil {
		// Invalid pattern, fall back to exact match
		return pattern == value
	}

	return matched
}

// matchesGroupPattern checks if a group pattern matches a group value.
// Unlike matchesPattern, empty pattern here means "core" API group (empty string),
// not "match all groups". Use "*" to match all groups.
func matchesGroupPattern(pattern, value string) bool {
	// Empty pattern means core API group (also empty string)
	if pattern == "" {
		return value == ""
	}

	return matchesPattern(pattern, value)
}

// formatStorePattern formats a store's GVK pattern for logging.
func formatStorePattern(store *v1alpha1.Store) string {
	group := store.Group
	if group == "" {
		group = "core"
	}

	return group + "/" + store.Version + "/" + store.Kind + "/" + store.Resource
}

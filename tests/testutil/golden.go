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

package testutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

const (
	// ResourceMetricsMonitorKind is the Kind string for RMM resources.
	ResourceMetricsMonitorKind = "ResourceMetricsMonitor"
)

// GoldenRule defines the structure of a golden rule for testing metric generation.
// Every field is required; no omitempty allowed, to ensure the test is fully specified.
type GoldenRule struct {
	Name        string                                 `yaml:"name"`
	Description string                                 `yaml:"description"`
	In          *unstructured.Unstructured             `yaml:"in"` // In is resource-agnostic to accommodate for any future resources introduced in RSM.
	Metrics     []string                               `yaml:"metrics"`
	Status      *v1alpha1.ResourceMetricsMonitorStatus `yaml:"status"`
}

// GoldenRulesFromYAML loads all golden rules from a (possibly multi-document) YAML file.
// Documents are separated by "---" lines; each document must have a non-empty name field.
func GoldenRulesFromYAML(_ context.Context, path string) ([]*GoldenRule, error) {
	data, err := os.ReadFile(EnsureSafePath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file %s: %w", path, err)
	}

	// Normalise Windows line endings, then split on YAML document separators.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	docs := bytes.Split(data, []byte("\n---\n"))

	var rules []*GoldenRule

	for _, doc := range docs {
		// The very first document may start with "---"; strip it.
		doc = bytes.TrimPrefix(bytes.TrimSpace(doc), []byte("---"))
		doc = bytes.TrimSpace(doc)

		if len(doc) == 0 {
			continue
		}

		goldenRule := &GoldenRule{}
		if err := yaml.Unmarshal(doc, goldenRule); err != nil {
			return nil, fmt.Errorf("failed to unmarshal YAML document in %s: %w", path, err)
		}

		// Skip documents that are all comments or whitespace (name will be empty).
		if goldenRule.Name == "" {
			continue
		}

		rules = append(rules, goldenRule)
	}

	return rules, nil
}

// ValidateUnstructuredGoldenRule validates the structure of a golden rule, ensuring all required fields are present.
func ValidateUnstructuredGoldenRule(rule *GoldenRule) error {
	if rule.Name == "" {
		return errors.New("golden rule has no name")
	}

	if rule.Description == "" {
		return fmt.Errorf("golden rule %s has no description", rule.Name)
	}

	if len(rule.Metrics) == 0 {
		return fmt.Errorf("golden rule %s has no metrics", rule.Name)
	}

	if rule.In == nil || rule.In.GetKind() != ResourceMetricsMonitorKind {
		return fmt.Errorf("golden rule %s has no RMM input resource", rule.Name)
	}

	return nil
}

// GetGoldenRuleFiles returns all golden rule file paths for the specified resolver types.
// baseDir is the directory containing resolver-type subdirectories (e.g. "golden" or "../golden").
func GetGoldenRuleFiles(baseDir string, resolverType []v1alpha1.ResolverType) []string {
	var files []string //nolint:prealloc

	for _, resolverType := range resolverType {
		goldenDir := filepath.Join(baseDir, string(resolverType))
		if _, err := os.Stat(goldenDir); os.IsNotExist(err) {
			panic(fmt.Sprintf("golden rules directory does not exist for resolver type %s: expected at %s", resolverType, goldenDir))
		}

		matches, _ := filepath.Glob(filepath.Join(goldenDir, "*.yaml"))
		files = append(files, matches...)
	}

	return files
}

// EnsureSafePath checks if the provided path is within the project directory to prevent file system access outside of the intended scope.
func EnsureSafePath(path string) string {
	cleanedPath := filepath.Clean(path)

	absolutePath, err := filepath.Abs(cleanedPath)
	if err != nil {
		panic(fmt.Sprintf("Failed to get absolute path: %v", err))
	}

	// Go two levels up from CWD to reach the project root, accommodating
	// tests running from tests/fake/ or tests/envtest/.
	projectDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(fmt.Sprintf("Failed to get absolute path of project directory: %v", err))
	}

	if !strings.HasPrefix(absolutePath, projectDir) {
		panic(fmt.Sprintf("Unsafe path detected: %s is outside of the project directory %s", absolutePath, projectDir))
	}

	return absolutePath
}

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
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

//go:embed testdata/validation_cases.yaml
var validationCasesYAML []byte

type validationTestCase struct {
	Name          string      `json:"name"`
	ShouldPass    bool        `json:"shouldPass"`
	Manifest      interface{} `json:"manifest"`
	ErrorContains []string    `json:"errorContains"`
}

type validationTestSuite struct {
	Valid          []validationTestCase `json:"valid"`
	RequiredFields []validationTestCase `json:"requiredFields"`
	Patterns       []validationTestCase `json:"patterns"`
	CEL            []validationTestCase `json:"cel"`
	Duplicates     []validationTestCase `json:"duplicates"`
}

func loadValidationTestSuite(t *testing.T) validationTestSuite {
	t.Helper()

	var suite validationTestSuite
	if err := yaml.Unmarshal(validationCasesYAML, &suite); err != nil {
		t.Fatalf("failed to unmarshal validation test suite: %v", err)
	}

	return suite
}

// validateResource performs full CRD validation on the provided object,
// running OpenAPI schema validation, CEL validations, and ListType map key checks.
func validateResource(
	t *testing.T,
	validator validation.SchemaValidator,
	celValidator *cel.Validator,
	structuralSchema *schema.Structural,
	obj interface{},
) []string {
	t.Helper()

	var errStrs []string

	// 1. OpenAPI structural schema validation (minLength, pattern, required, etc.)
	schemaResult := validator.Validate(obj)
	for _, e := range schemaResult.Errors {
		errStrs = append(errStrs, e.Error())
	}

	// 2. CEL validations
	if celValidator != nil {
		celErrs, _ := celValidator.Validate(
			context.Background(),
			field.NewPath("root"),
			structuralSchema,
			obj,
			nil,
			celconfig.RuntimeCELCostBudget,
		)
		for _, e := range celErrs {
			errStrs = append(errStrs, e.Error())
		}
	}

	// 3. ListType map unique key validation
	if mapObj, ok := obj.(map[string]interface{}); ok && structuralSchema != nil {
		listtypeErrs := listtype.ValidateListSetsAndMaps(field.NewPath("root"), structuralSchema, mapObj)
		for _, e := range listtypeErrs {
			errStrs = append(errStrs, e.Error())
		}
	}

	return errStrs
}

// setupValidator loads the CRD and initializes schema, CEL, and ListType validators.
func setupValidator(t *testing.T) (validation.SchemaValidator, *cel.Validator, *schema.Structural) {
	t.Helper()

	manifestPath := filepath.Join("..", "..", "..", "..", "manifests", "custom-resource-definition.yaml")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("failed to read CRD manifest: %v", err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("failed to unmarshal CRD: %v", err)
	}

	v1Schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema

	internalSchema := &apiextensions.JSONSchemaProps{}
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v1Schema, internalSchema, nil); err != nil {
		t.Fatalf("failed to convert schema: %v", err)
	}

	structuralSchema, err := schema.NewStructural(internalSchema)
	if err != nil {
		t.Fatalf("failed to convert to structural schema: %v", err)
	}

	validator, _, err := validation.NewSchemaValidator(internalSchema)
	if err != nil {
		t.Fatalf("failed to create schema validator: %v", err)
	}

	celValidator := cel.NewValidator(structuralSchema, true, celconfig.PerCallLimit)
	if celValidator == nil {
		t.Fatalf("failed to create CEL validator")
	}

	return validator, celValidator, structuralSchema
}

func TestCRDValidation_Valid(t *testing.T) {
	t.Parallel()

	validator, celValidator, structuralSchema := setupValidator(t)
	suite := loadValidationTestSuite(t)

	for _, tt := range suite.Valid {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			runValidationTestCase(t, validator, celValidator, structuralSchema, tt.Manifest, tt.ShouldPass, tt.ErrorContains)
		})
	}
}

func TestCRDValidation_RequiredFields(t *testing.T) {
	t.Parallel()

	validator, celValidator, structuralSchema := setupValidator(t)
	suite := loadValidationTestSuite(t)

	for _, tt := range suite.RequiredFields {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			runValidationTestCase(t, validator, celValidator, structuralSchema, tt.Manifest, tt.ShouldPass, tt.ErrorContains)
		})
	}
}

func TestCRDValidation_Patterns(t *testing.T) {
	t.Parallel()

	validator, celValidator, structuralSchema := setupValidator(t)
	suite := loadValidationTestSuite(t)

	for _, tt := range suite.Patterns {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			runValidationTestCase(t, validator, celValidator, structuralSchema, tt.Manifest, tt.ShouldPass, tt.ErrorContains)
		})
	}
}

func TestCRDValidation_CEL(t *testing.T) {
	t.Parallel()

	validator, celValidator, structuralSchema := setupValidator(t)
	suite := loadValidationTestSuite(t)

	for _, tt := range suite.CEL {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			runValidationTestCase(t, validator, celValidator, structuralSchema, tt.Manifest, tt.ShouldPass, tt.ErrorContains)
		})
	}
}

func TestCRDValidation_Duplicates(t *testing.T) {
	t.Parallel()

	validator, celValidator, structuralSchema := setupValidator(t)
	suite := loadValidationTestSuite(t)

	for _, tt := range suite.Duplicates {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			runValidationTestCase(t, validator, celValidator, structuralSchema, tt.Manifest, tt.ShouldPass, tt.ErrorContains)
		})
	}
}

func runValidationTestCase(
	t *testing.T,
	validator validation.SchemaValidator,
	celValidator *cel.Validator,
	structuralSchema *schema.Structural,
	obj interface{},
	shouldPass bool,
	errorContains []string,
) {
	t.Helper()

	errStrs := validateResource(t, validator, celValidator, structuralSchema, obj)

	if shouldPass {
		if len(errStrs) > 0 {
			t.Fatalf("expected validation to pass, but got errors:\n%s", strings.Join(errStrs, "\n"))
		}

		return
	}

	if len(errStrs) == 0 {
		t.Fatalf("expected validation to fail, but it passed")
	}

	for _, expected := range errorContains {
		if !hasErrorContaining(errStrs, expected) {
			t.Errorf("expected error containing %q, but got:\n%s", expected, strings.Join(errStrs, "\n"))
		}
	}
}

func hasErrorContaining(errStrs []string, expected string) bool {
	for _, errStr := range errStrs {
		if strings.Contains(errStr, expected) {
			return true
		}
	}

	return false
}

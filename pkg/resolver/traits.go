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

package resolver

// Traits describes what every resolver declares about itself.
type Traits struct {
	ObjectBinding string
	Sanitization  SanitizationSpec
	Bounds        BoundsSpec
}

// ExpressionTraits adds which result kinds an expression resolver can return.
type ExpressionTraits struct {
	Traits
	Lists bool
	Maps  bool
}

// SanitizationSpec says whether the resolver's helpers sanitize label keys.
type SanitizationSpec struct {
	LabelKeys bool
}

// BoundsSpec says which limits the resolver applies to one evaluation.
type BoundsSpec struct {
	Timeout   bool
	CostLimit bool
	StepLimit bool
}

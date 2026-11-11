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

// Kind identifies the type of a resolved value.
type Kind int

const (
	KindInvalid Kind = iota
	KindScalar
	KindList
	KindMap
)

// Value holds the result of an expression evaluation.
// Only the field corresponding to Kind is populated.
// Empty collections are non-nil, and null values are represented as "<nil>".
type Value struct {
	Kind   Kind
	Scalar string
	List   []Value
	Map    map[string]Value
}

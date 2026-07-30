// Copyright 202X [Name of Organization or Project]
// Licensed under the [License Name, e.g., Apache License, Version 2.0]
// (Copy the exact header text from another file in your repository!)

package resolver

import "context"

// Resolver defines the granular behaviors and traits for evaluating a given expression.
type Resolver interface {
    // 1. Data Type Signatures

    // ResolveScalar resolves the given expression to a flat key-value pair.
    ResolveScalar(ctx context.Context, query string, obj map[string]interface{}) (map[string]string, error)

    // ResolveComposite handles family-level or complex nested resolutions.
    ResolveComposite(ctx context.Context, query string, obj map[string]interface{}) ([]ResolvedFamily, error)

    // 2. Behavioral Traits
    SanitizeKey(key string) string
    SupportsUnderscoreExpansion() bool
}
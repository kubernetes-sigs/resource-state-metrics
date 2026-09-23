# Proposal-15: Granular Resolver interface

|               |                                                                     |
|---------------|---------------------------------------------------------------------|
| **Authors**   | @emmayusufu                                                         |
| **Status**    | provisional                                                         |
| **Created**   | 2026-09-16                                                          |
| **Updated**   | 2026-09-23                                                          |
| **Reviewers** | @rexagod, @mrueg                                                    |
| **Approvers** | TBD                                                                 |
| **See also**  | https://github.com/kubernetes-sigs/resource-state-metrics/issues/15 |
| **Replaces**  | -                                                                   |

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [The current interface is too small](#the-current-interface-is-too-small)
  - [Starlark follows a different path](#starlark-follows-a-different-path)
  - [Inputs are different](#inputs-are-different)
  - [Results are flattened into strings](#results-are-flattened-into-strings)
  - [Failures are not represented consistently](#failures-are-not-represented-consistently)
  - [Sanitization is also split](#sanitization-is-also-split)
  - [Timeouts are implemented differently](#timeouts-are-implemented-differently)
  - [Helper functions have different behaviour](#helper-functions-have-different-behaviour)
  - [Metrics are inconsistent](#metrics-are-inconsistent)
  - [Possible existing bug](#possible-existing-bug)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [Typed results](#typed-results)
  - [Explicit errors](#explicit-errors)
  - [Resolver traits](#resolver-traits)
  - [Shared timeout handling](#shared-timeout-handling)
  - [Shared helper behaviour](#shared-helper-behaviour)
  - [Shared tests](#shared-tests)
  - [Wrapper changes](#wrapper-changes)
  - [Migration](#migration)
  - [Open questions](#open-questions)
  - [User Stories](#user-stories)
    - [Story 1](#story-1)
    - [Story 2](#story-2)
  - [Notes/Constraints/Caveats (Optional)](#notesconstraintscaveats-optional)
  - [Risks and Mitigations](#risks-and-mitigations)
    - [Existing output changes](#existing-output-changes)
    - [Conflicts with other work](#conflicts-with-other-work)
    - [Large change](#large-change)
- [Design Details](#design-details)
  - [Errors](#errors)
  - [Wrapper](#wrapper)
  - [Migration](#migration-1)
  - [Test Plan](#test-plan)
    - [Unit tests](#unit-tests)
    - [Golden tests](#golden-tests)
    - [e2e tests](#e2e-tests)
  - [Graduation Criteria](#graduation-criteria)
  - [Observability &amp; operational impact](#observability--operational-impact)
- [Implementation History](#implementation-history)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
  - [Keep the existing interface and document the behaviour](#keep-the-existing-interface-and-document-the-behaviour)
  - [Fix each resolver independently](#fix-each-resolver-independently)
  - [Make Starlark implement the existing string map interface](#make-starlark-implement-the-existing-string-map-interface)
<!-- /toc -->

## Summary

resource-state-metrics currently has three resolvers: `unstructured`, CEL and Starlark.

There is one `Resolver` interface for them, but in practice only `unstructured` and CEL implement it. Starlark has a different method signature, does not implement the interface at all, and is handled separately by the wrapper.

The existing interface is also too small to describe the behaviour we actually depend on. Lists, maps, errors, label sanitization, timeouts, helper functions and metrics are handled in different places, and some of those behaviours are different between resolvers.

This proposal introduces two interfaces:

- `ExpressionResolver` for query based resolvers such as `unstructured` and CEL.
- `FamilyResolver` for resolvers such as Starlark that produce complete metric families.

The proposal also moves the shared behaviour into the resolver contracts instead of relying on conventions spread across `pkg/resolver` and `internal/`.

The main goals are to make resolver results typed, make failures explicit, document resolver capabilities, and make it easier to add another resolver without having to discover all of these conventions from the existing implementation.

The existing `ResourceMetricsMonitor` behaviour should remain unchanged. Existing configurations should produce the same metric output before and after the migration.

## Motivation

Line references below are against `main` at `3b06a2a` on 2026-09-22. `pkg/resolver` last changed on 2026-05-29 (`8398f54`), so nothing here is racing a change in flight.

### The current interface is too small

`resolver.go:25` currently defines:

```go
Resolve(query string, unstructuredObjectMap map[string]interface{}) map[string]string
```

There are comments describing some of the expected behaviour, including the `list_name#index` convention at line 24, and there is a TODO at line 28 around exposing resolver traits.

That means some fairly important behaviour is currently part of an implicit contract rather than the interface itself.

### Starlark follows a different path

`unstructured` and CEL implement `Resolver` (`unstructured.go:33`, `cel.go:53`).

Starlark does not. It has:

```go
func (sr *StarlarkResolver) Resolve(obj map[string]interface{}) ([]ResolvedFamily, error)
```

in `starlark.go:89`, and there is no interface assertion. I confirmed that with a type assertion in a test.

The wrapper therefore has a separate Starlark path, `f.starlarkResolver.Resolve` at `family.go:230` against `resolverInstance.Resolve` at `family.go:299` and `family.go:334`. A new resolver author has to understand this distinction before deciding which pattern to follow.

### Inputs are different

The resolvers also expose different object bindings.

`unstructured` treats the query as a path and splits it on `.` (`unstructured.go:45`).

CEL exposes the object as `o` (`cel.go:317`).

Starlark exposes it as `obj` (`starlark.go:141`).

These differences are not currently represented in the resolver interface.

### Results are flattened into strings

The current interface returns `map[string]string`.

For CEL, lists are represented using keys such as (`cel.go:411`):

```text
fieldParent#0
fieldParent#1
```

The wrapper then parses those keys again using `listIndexRegex` (`family.go:57`). That regex is `.+#\d+` and it is unanchored, so a key like `foo#2bar` matches too even though it is not a list entry.

Expanded values also use a `"\x00"` sentinel key (`family.go:48`).

This means the resolver produces a flattened representation and the wrapper has to reconstruct the structure later.

`unstructured` does not support the same list and map behaviour. It returns `Sprintf("%v")` of whatever it found (`unstructured.go:60`), so a map comes out as Go syntax like `map[a:b]`. Starlark returns `ResolvedFamily` directly.

I think the resolver should return the type it actually resolved instead of encoding that type into a string map.

### Failures are not represented consistently

`unstructured` returns the query itself when a field is missing or an error occurs (`unstructured.go:50`, `unstructured.go:57`).

CEL follows a similar approach through `defaultMapping` (`cel.go:127`, `cel.go:142`, `cel.go:359`), including parse errors, evaluation errors, cost limit failures and timeouts.

Starlark returns a Go error (`starlark.go:121`).

The self mapping also leaks into output. When a label expression names a field the object does not have, the wrapper takes the scalar branch because the query key is present (`family.go:338`), and the label is emitted with the expression text as its value, for example `absent="metadata.nothere"`. I confirmed this with a test against `resolveLabels`. For metric values the same self mapping fails to parse as a number at write time and the sample is skipped, which is what the resolver log messages mean by skipped at write time. Labels have no such check.

This means the wrapper cannot reliably distinguish:

- field not found
- invalid expression
- evaluation failure
- timeout
- budget or cost limit exceeded

The new contract should make these cases explicit.

### Sanitization is also split

`metricutil.SanitizeLabelKey` (`pkg/metricutil/metrics.go:62`) is used by CEL's `labelPrefix` (`cel.go:262`) and Starlark's `label_prefix` (`starlark.go:241`).

The wrapper has another sanitization path, `sanitizeKey` in `family.go:468`, using `strcase.ToSnake`. It is applied at `family.go:253`, `family.go:341`, `family.go:353`, `family.go:366` and `family.go:368`.

For example, `envType` can remain `envType` through one path but become `env_type` through another. I confirmed this by running both functions in a test. A key can also pass through both.

This behaviour should have one defined rule instead of depending on which path produced the value.

### Timeouts are implemented differently

CEL has a timeout and cost limit (`cel.go:115` to `cel.go:142`, `cel.go:311`).

Starlark has a timeout and step limit (`starlark.go:102`).

The implementations also differ in cancellation behaviour. CEL calls `program.Eval` without a context (`cel.go:317`), so when the timeout fires the goroutine can continue running until evaluation finishes or the cost limit is reached.

Starlark cancels its thread when the timeout is reached (`starlark.go:119`).

`unstructured` does not need the same kind of execution bound because it is walking an object.

There is an opportunity here to share the timeout and metrics handling while still allowing each resolver to have its own execution limits.

### Helper functions have different behaviour

There are equivalent helpers implemented separately for CEL and Starlark.

For example, CEL's `quantity` (`cel.go:238`) returns `0.0` for an empty string, while Starlark's `quantity_to_float` (`starlark.go:167`) returns an error.

`labelPrefix` (`cel.go:262`) and `label_prefix` (`starlark.go:241`) also perform similar work under different names. CEL has `unixSeconds` and `now` (`cel.go:183`, `cel.go:204`), Starlark imports the whole Starlark time module (`starlark.go:133`).

If both resolvers are expected to provide the same functionality, the expected behaviour should be written down and tested in one place.

### Metrics are inconsistent

Currently only CEL records resolver evaluation metrics, through `cel_evaluations_total` (`internal/controller.go:195`, `cel.go:124`, `cel.go:131`, `cel.go:139`).

Starlark and `unstructured` do not expose the same information.

The proposal is to make resolver evaluation metrics consistent rather than having them depend on which resolver happens to be used.

### Possible existing bug

While looking at the map handling, I found a possible issue in `resolveMapInner` (`cel.go:425`).

It accepts:

```text
string
int
uint
float64
bool
```

but not `int64` or `uint64`.

`resolveListInner` (`cel.go:410`) does handle those integer types.

CEL can return `int64` and `uint64`, so integer values inside maps may currently be skipped.

I have not added a failing test for this yet, so I am treating it as a candidate bug rather than part of the proposal itself.

### Goals

- Put shared resolver behaviour in the resolver contract.
- Return lists and maps as actual lists and maps rather than encoded strings.
- Make resolver failures explicit.
- Give each resolver a way to declare its capabilities and limits.
- Make it easier to add another resolver.
- Keep existing `ResourceMetricsMonitor` output unchanged.

### Non-Goals

- Changing the `ResourceMetricsMonitor` API.
- Changing any CRD fields.
- Adding another resolver as part of this proposal.
- Changing emitted values for existing configurations, with one documented exception, missing label fields no longer carry the expression text as their value.
- Doing performance work inside the resolvers.
- Standardising every difference between CEL and Starlark immediately.

## Proposal

I propose replacing the current `Resolver` interface with two interfaces.

`ExpressionResolver` is for resolvers that take a query and resolve a value from one object.

`FamilyResolver` is for resolvers that execute something against an object and produce complete metric families.

The first two implementations would be `unstructured` and CEL for `ExpressionResolver`, with Starlark implementing `FamilyResolver`.

### Typed results

Instead of returning everything through `map[string]string`, the expression resolver would expose one `ResolveValue` method that returns a tagged value, a found flag and an error. The value says whether it is a scalar, a list or a map, and lists and maps can hold values of any of the three kinds.

One method rather than one per kind because the wrapper cannot know the kind up front. `Metric.Value` and `Label.Value` are expression strings, and CEL only learns the result type after evaluating (`cel.go:347`). Calling a typed method per kind would mean evaluating the same expression more than once, which breaks `now()` and inflates the cost accounting.

The list index convention and expanded-value sentinel would no longer be needed.

The wrapper would receive a list as a list and a map as a map, and it keeps doing the expansion. Nested lists and maps come back nested, so the flattening CEL does today (`cel.go:410` to `cel.go:434`) moves into the wrapper as a written down rule with a golden fixture, instead of disappearing. Whether that flattening should stay long term is an open question below.

### Explicit errors

Resolver failures should be returned as Go errors that the wrapper can tell apart with `errors.Is`.

The wrapper can then decide what to do based on the error rather than treating an error as a missing value.

### Resolver traits

The existing TODO around resolver behaviour can become an explicit `Traits()` method.

This gives the wrapper and tests a way to understand what a resolver supports without relying on implementation details.

For example:

- CEL uses `o` as its object binding.
- Starlark uses `obj`.
- `unstructured` has no binding at all, its query is a dotted path.
- CEL has a timeout and cost limit.
- Starlark has a timeout and step limit.
- `unstructured` does not need an execution timeout.
- CEL can return lists and maps, so list and map expansion work with it. `unstructured` only returns scalars.

That last one is the underscore expansion trait #15 asks for. Today expansion is a wrapper decision made after evaluation, a non scalar result plus a leading `_` on the label name means map expansion (`family.go:346`), and nothing tells the wrapper whether the resolver can produce a list or a map at all. The trait says which of the two a resolver can return. It cannot be checked at config load, since the result kind is only known after evaluation, so it is used in three places instead: the shared test table skips list and map cases for resolvers that declare neither, the docs for each resolver state it, and the wrapper logs a clear error if a resolver returns a kind it did not declare.

### Shared timeout handling

Timeout handling and resolver evaluation metrics should be moved into shared code where possible.

Every resolve method takes a `context.Context`. The shared wrapper owns the timeout and cancels the context, and each resolver stops when the context is done. CEL and Starlark still provide their own limits, cost and steps.

This is what fixes the current CEL timeout behaviour where evaluation can continue after the wrapper timeout has fired. Stopping waiting is not the same as stopping the work, so cancellation has to be part of the contract, not only the wrapper.

### Shared helper behaviour

The equivalent functions in CEL and Starlark should have documented behaviour, and the shared tests can only cover a function once both sides have it. Today the pairs are:

- `quantity` in CEL and `quantity_to_float` in Starlark. Same job, different name, and they disagree on an empty string.
- `labelPrefix` in CEL and `label_prefix` in Starlark. Same job, different name.
- `unixSeconds` and `now` in CEL have no Starlark equivalent. Starlark gets the whole `time` module instead (`starlark.go:133`), with `time.now()`, `time.parse_time()` and friends, which is a different and larger surface.

So the shared table covers the first two pairs from the start, with the edge cases written down, an empty quantity being the obvious one. For time, the proposal is to add `unix_seconds(s)` and `now()` built ins to Starlark that mirror the CEL functions, and keep the `time` module as it is, so the time rows can run against both. The names differ per DSL because each follows its own naming convention, the table maps them.

### Shared tests

A shared test table should exercise the common resolver behaviour.

The table can cover:

- scalar values
- lists
- maps
- missing fields
- invalid expressions
- timeouts
- cost or step limits
- label sanitization
- number formatting
- helper functions

Each resolver then runs the same cases where the behaviour is expected to be shared.

This also gives a new resolver a clear way to demonstrate that it implements the contract.

### Wrapper changes

Most of the wrapper changes should be around `resolveMetricValue` and `resolveLabels` in `internal/family.go`.

The wrapper should work with the typed resolver results rather than reconstructing lists and maps from encoded keys.

Once all resolvers have moved over, the following code can be removed:

- `listIndexRegex`
- `expandedValueSentinel`
- `collectIndexedResolvedValues`
- the old special-case handling for Starlark

### Migration

The migration should happen in stages, one PR each, with the existing golden tests run at every stage. The stages are listed under Design Details.

The old `Resolver` interface and the old `Resolve` methods stay for one release, deprecated, so that the change does not have to land all at once.

### Open questions

<<[UNRESOLVED @rexagod @mrueg ]>>

- Is making Starlark's contract official as `FamilyResolver` part of #15, or should #15 only cover expression resolvers and leave the family contract for a follow-up?
- Nested lists and maps are flattened into the label map by CEL today (`cel.go:415`). The proposal keeps that behaviour, done by the wrapper on the nested value, so output does not change. Should it stay long term, or should nested composites be rejected with a clear error once existing configs have been checked for them?

<<[/UNRESOLVED]>>

### User Stories

#### Story 1

An operator uses CEL and Starlark to resolve the same field from a resource.

Today the same numeric value can be formatted differently depending on the resolver. For example, one resolver may produce `1e+06` while another produces `1000000`.

With the new contract, each resolver's current formatting is pinned by the shared tests, so picking one shared rule later is a documented change instead of a surprise.

#### Story 2

A contributor wants to add another resolver.

Today they need to understand the existing interface, the list encoding, the wrapper's special cases, the sanitization behaviour, the timeout implementations and the resolver-specific metrics.

With the new design, they implement one of the resolver contracts, define its traits and run the shared resolver tests.

### Notes/Constraints/Caveats (Optional)

- `pkg/resolver` is under `pkg/`, so it is importable from outside the repository. That makes removing the exported `Resolver` interface a source compatibility break for anyone outside this repo who implements or consumes it.
- So the new method has a new name, `ResolveValue`, and the concrete types keep the old `Resolve(query, obj) map[string]string` for one release, marked deprecated and implemented by calling `ResolveValue` and re-encoding the result in the old `#index` form. That keeps both kinds of outside caller compiling, code that holds a `resolver.Resolver` and code that calls `Resolve` on `*CELResolver` or `*UnstructuredResolver` directly. `StarlarkResolver.Resolve(obj)` stays the same way next to `ResolveFamilies`. The old interface and the old methods go in the release after. Anything in this repo moves to the new contracts straight away.
- `internal/family.go` is currently the only caller in the repository.
- Starlark already produces complete families, so `FamilyResolver` mostly makes the existing behaviour explicit.
- Float formatting will remain resolver-specific initially. Changing it could alter existing metric output, so that should be handled separately.
- Sanitization keeps today's two step behaviour in this proposal. The resolvers' own helpers keep applying `metricutil.SanitizeLabelKey`, and the wrapper keeps applying `sanitizeKey` to label names and to every key it receives, exactly as now, pinned by the golden fixtures. Collapsing that into one function changes output whichever rule wins, `envType` stays `envType` under one and becomes `env_type` under the other, so that is a follow up with its own fixture, not part of this. `Traits.Sanitization` records which helper a resolver applies so the follow up has the facts.
- Existing output compatibility is more important than making every resolver behave identically immediately.

### Risks and Mitigations

#### Existing output changes

This is the biggest risk.

Existing golden tests under `tests/golden` should continue to pass byte for byte. New fixtures should be added for lists, maps and missing fields before changing the wrapper.

#### Conflicts with other work

Small standalone fixes that are already in progress should be allowed to land first where possible. The proposal should then build on the current behaviour rather than trying to combine unrelated changes into one PR.

#### Large change

This touches the three resolvers and the wrapper.

The migration is therefore split into the smaller PRs listed under Design Details, so that each change stays easy to review.

## Design Details

The proposed contracts are:

```go
type Kind int

const (
	KindInvalid Kind = iota // the zero value, never a valid result
	KindScalar
	KindList
	KindMap
)

type Value struct {
	Kind   Kind
	Scalar string           // set when Kind is KindScalar
	List   []Value          // set when Kind is KindList
	Map    map[string]Value // set when Kind is KindMap
}

type ExpressionResolver interface {
	ResolveValue(ctx context.Context, query string, obj map[string]any) (Value, bool, error)
	Traits() ExpressionTraits
}

type FamilyResolver interface {
	ResolveFamilies(ctx context.Context, obj map[string]any) ([]ResolvedFamily, error)
	Traits() Traits
}
```

`Value` is a small tree. A scalar is a leaf, a list holds values, a map holds values by key, and the wrapper switches on `Kind`. This is what lets one `ResolveValue` call carry whatever the expression produced, including the nested shapes CEL handles today, without the resolver having to guess what the caller wanted.

The rules for the three return values, so the deprecated wrappers cannot change output by reading them differently:

- `found` is `false` only when the query points at a field the object does not have. Then `Value` is the zero value and the caller ignores it, and `err` is `nil`. This is a deliberate change from today for labels, where a missing field is emitted with the expression text as its value. That is treated as a bug and the new behaviour is pinned by a golden fixture, see the test plan. It is the one place this proposal changes output on purpose.
- A `null` value is `found = true` with `Kind = KindScalar` and `Scalar = "<nil>"`. That is what CEL emits today (`cel.go:354`) and changing it would alter existing output. Whether `null` should stay a value is a follow up, not part of this.
- An empty list or an empty map is a successful result. `found` is `true`, `Kind` is `KindList` or `KindMap`, and `List` or `Map` is empty, not `nil`.
- `err` is non `nil` only for a failure to evaluate. Then `found` is `false` and `Value` is the zero value.
- The zero `Kind` is `KindInvalid`, so `Value{}` is never a valid result. An empty string scalar is `Kind = KindScalar` with `Scalar = ""`, which is distinguishable from the zero value. `Kind` is always one of the three real kinds when `found` is true.

Every resolve method takes a context. The shared timeout wrapper derives a context with the resolver's timeout and cancels it, and each resolver is responsible for stopping when the context is done. That is what makes the wrapper able to stop work rather than just stop waiting:

- CEL uses `Program.ContextEval` instead of `Program.Eval`, and the program is built with `cel.InterruptCheckFrequency` set. cel-go only checks the context when that option is non zero, and `cel.go` does not set it today, so both changes are needed. Both exist in cel-go `v0.30.0`, the version in `go.mod`. One limit to be clear about, cel-go checks for interruption inside comprehensions only, `map`, `filter`, `all`, `exists` and the like, every N iterations. A long straight line expression with no loop is not interruptible and stays bounded by the cost limit alone. That covers the slow cases that exist in practice, which are loops over big lists, and it is still strictly better than today, where nothing is interruptible.
- Starlark keeps `thread.Cancel`, called from a goroutine that waits on `ctx.Done()`. That goroutine also waits on a done channel closed when the script returns, so a successful run under a long lived context does not leave it behind. The shared wrapper derives a per call context with `context.WithTimeout` and cancels it on return in any case, so nothing outlives the call.
- `unstructured` ignores the context, since walking a map does not block.

`unstructured` and CEL implement `ExpressionResolver`.

Starlark implements `FamilyResolver`.

The common resolver information can be represented as:

```go
// Traits is what every resolver declares.
type Traits struct {
	ObjectBinding string
	Sanitization  SanitizationSpec
	Bounds        BoundsSpec
}

// ExpressionTraits adds what only makes sense for a resolver that
// returns a Value. A family resolver has no Value.Kind to declare.
type ExpressionTraits struct {
	Traits
	Lists bool // can ResolveValue return KindList
	Maps  bool // can ResolveValue return KindMap
}
```

`Lists` and `Maps` are the expansion trait, split because a resolver may well support one and not the other. Both `true` for CEL, both `false` for `unstructured`. They live on `ExpressionTraits` and not on `Traits` because a family resolver returns whole families, there is no result kind for it to declare, and it should not have to publish meaningless flags. They are not a load time check, the result kind is only known after evaluation. They drive the shared test table, the docs, and a wrapper error if a resolver returns a kind it did not declare.

The exact shape of `SanitizationSpec` and `BoundsSpec` can be worked out during implementation.

### Errors

Errors should be typed or wrapped so callers can use `errors.Is`, for example:

```go
var (
	ErrInvalidExpression = errors.New("invalid expression")
	ErrBudgetExceeded    = errors.New("resolver budget exceeded")
)
```

At minimum, the wrapper needs to distinguish:

- invalid expression
- budget exceeded
- evaluation failure

A cancellation that comes from the caller, for example the controller shutting down, is returned as the context's own error, `context.Canceled`. It is neither a budget error nor an evaluation failure, and the wrapper does not count it in the evaluations metric, so a shutdown does not show up as a burst of timeouts.

A missing value is not an error. It is `found = false` with a `nil` error, see the rules above.

The exact error types are still open to implementation.

### Wrapper

The wrapper calls `ResolveValue` once and switches on `Value.Kind`. A scalar is one label value, a list is list expansion, a map is map expansion when the label name starts with `_` and key concatenation otherwise, the same rules as today, now applied to a typed value.

Two cases that exist today and keep their behaviour:

- An empty list or empty map as a metric value means zero samples for that object, not an error, which is what `family.go:301` does now for an empty result. As a label it means the label is left off the series, as now.
- A missing metric value, `found = false`, skips the sample and logs it at verbosity 1, which is what happens today when the self mapped query fails to parse as a number at write time. Only the log message changes, it can now say the field was missing instead of failing a float parse.

Nested values are flattened by the wrapper the way CEL flattens them today, so existing output does not change. That flattening gets its own golden fixture so the behaviour is pinned before it moves.

For example, a list result can be paired with label definitions by position instead of first becoming:

```text
foo#0
foo#1
foo#2
```

and then being parsed again.

### Migration

1. Land the CEL timeout cancellation fix as its own small PR, since it stands on its own.
2. Add the new interfaces and the deprecated wrappers without changing behaviour.
3. Move `unstructured` to the new interface.
4. Move CEL to the new interface.
5. Move Starlark to `FamilyResolver`.
6. Move the wrapper to the typed results.
7. Add the resolver evaluation counter, update the mixin alerts and regenerate the manifests, keep the old metric name as an alias for one release.
8. One release later, remove the deprecated `Resolver` interface, the deprecated `Resolve` methods, and the metric alias.

The existing golden tests run at every stage. During migration, the old `Resolve` methods stay on the concrete types as deprecated wrappers around `ResolveValue`, so the old `Resolver` interface keeps working. This gives us a way to introduce the new contract without changing every caller in one PR.

### Test Plan

#### Unit tests

Add a shared resolver test table under `pkg/resolver`.

Each resolver should run the shared cases where applicable.

Additional resolver-specific tests should cover:

- error types
- cancellation
- limits
- traits
- resolver-specific behaviour

Sanitization should have its own tests under `pkg/metricutil`.

Wrapper behaviour should continue to be tested under `internal`.

#### Golden tests

Existing fixtures under `tests/golden/cel` and `tests/golden/starlark` should continue to pass unchanged, checked with `make compare_metrics`.

New fixtures should cover:

- list expansion
- map expansion
- nested lists and maps, pinning today's flattening before it moves into the wrapper
- a null field, pinning `<nil>`
- a missing label field, pinning the new behaviour where the label is dropped instead of carrying the expression text. Unlike the others this fixture lands in the same PR as the wrapper change, since it expects the new output
- resolver errors

The important requirement is that existing configurations produce byte identical output, with the missing label field fixture as the one documented exception.

#### e2e tests

Existing `make test_e2e` coverage should remain.

Add at least one `ResourceMetricsMonitor` example for each resolver that exercises list and map behaviour where supported.

### Graduation Criteria

The proposal is ready for implementation once the maintainers agree on the two contracts and the open questions have been resolved.

The implementation is complete when:

- all three existing resolvers use the new contracts
- the shared resolver tests run in CI
- the wrapper no longer depends on encoded list keys
- resolver failures are distinguishable
- the existing golden fixtures pass unchanged
- the deprecated `Resolver` interface and the deprecated `Resolve` methods have been removed after the agreed migration period

### Observability & operational impact

Resolver evaluation metrics should become consistent across resolvers.

The current `cel_evaluations_total` has `namespace`, `name`, `family` and `result` labels. It becomes a general resolver evaluation counter, `resolver_evaluations_total`, with a `resolver` label added, one value per resolver, and `result` keeping its current values, `success`, `error` and `timeout`.

Two alerts in the mixin query the old name, `ResourceStateMetricsCELEvaluationErrors` and `ResourceStateMetricsCELEvaluationTimeouts`. The source is `jsonnet/resource-state-metrics-mixin/alerts.libsonnet:40` to `alerts.libsonnet:60` and the generated copy is `jsonnet/manifests/alerts.yaml:31` to `alerts.yaml:52`. Renaming the metric without touching them would switch those alerts off silently. So the PR that adds the new counter also updates the libsonnet to query it with `resolver="cel"`, regenerates the manifests with `make generate`, and keeps the old metric name emitting for one release as an alias so dashboards outside this repo have time to move. The alias goes in the release after.

This adds a small amount of cardinality to an internal metric and gives operators a consistent way to see resolver failures.

The wrapper logs can also include the resolver and error kind instead of silently treating every failure as a missing value.

## Implementation History

- 2026-08-13, issue #15 asks for a more granular interface.
- 2026-09-07, behaviours spec shared with the maintainers.
- 2026-09-16, proposal drafted.
- 2026-09-22, opened as provisional in #112.

## Drawbacks

This is a fairly large refactor for a young package.

It touches all three resolvers and the wrapper, and some of the existing behaviour is going to have to be made explicit before the interfaces can be finalised.

The migration plan is intended to keep the individual PRs small, but the overall change is still significant.

## Alternatives

### Keep the existing interface and document the behaviour

This would be the smallest change, but it leaves important behaviour outside the interface.

The current comments already describe some of the conventions, but Starlark, CEL and `unstructured` do not consistently follow them.

### Fix each resolver independently

Another option is to fix the current differences one PR at a time.

That would address individual problems, but it does not give us a contract that prevents the same drift from happening again.

It also does not solve the underlying problem with the current `map[string]string` interface.

### Make Starlark implement the existing string map interface

I do not think this fits Starlark well.

Starlark already produces complete `ResolvedFamily` values. Forcing that into `map[string]string` would throw away structure and make the wrapper responsible for reconstructing it.

Keeping Starlark as a `FamilyResolver` makes that distinction explicit instead.

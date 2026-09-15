<!--
**Note:** When your proposal is complete, all of these HTML comment blocks
should be removed.

To get started with this template:

- [ ] **Make a copy of this template directory.**
  Copy this directory to `proposals/NNNN-<short-descriptive-title>/`, where
  `NNNN` is the tracking issue number (with no leading-zero padding) for your
  proposal.
- [ ] **Fill in the metadata table below.**
  At minimum: title, authors, status, and the creation date.
- [ ] **Fill out this file as best you can.**
  At minimum, the "Summary" and "Motivation" sections. These should be easy if
  you have pre-flighted the idea with the resource-state-metrics maintainers.
- [ ] **Open a pull request for this proposal.**
  Merge early and iterate: aim to get the goals clarified and merged quickly,
  then fill out the details in follow-up pull requests.
- [ ] **Regenerate the table of contents.**
  Run `make proposals_toc` so the generated table-of-contents block stays in
  sync with your headings.

Just because a proposal is merged does not mean it is complete or approved. A
proposal marked as `provisional` is a working document and subject to change.
You can denote sections that are under active debate as follows:

```
<<[UNRESOLVED optional short context or usernames ]>>
Stuff that is being argued.
<<[/UNRESOLVED]>>
```

One proposal corresponds to one "feature" or "enhancement" for its whole
lifecycle. If new details emerge that belong in the proposal, edit it. Once a
feature has become "implemented", major changes should get a new proposal.
-->

# Proposal-NNNN: Your short, descriptive title

<!--
Keep the title short, simple, and descriptive. A good title helps communicate
what the proposal is and is considered as part of any review.
-->

<!--
Metadata for this proposal. Keep it in sync as the proposal moves through its
lifecycle.

- status: one of provisional | implementable | implemented | rejected |
  withdrawn | replaced
- authors/reviewers/approvers: GitHub handles, e.g. @octocat
- see-also / replaces: links to related proposals, e.g.
  proposals/0001-example/README.md
-->

|               |              |
|---------------|--------------|
| **Authors**   | @your-handle |
| **Status**    | provisional  |
| **Created**   | yyyy-mm-dd   |
| **Updated**   | yyyy-mm-dd   |
| **Reviewers** | TBD          |
| **Approvers** | TBD          |
| **See also**  | -            |
| **Replaces**  | -            |

<!--
A table of contents is helpful for quickly jumping to sections of a proposal.
Keep the marker block below in place and regenerate it with
`make proposals_toc`.
-->

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
    - [Story 1](#story-1)
    - [Story 2](#story-2)
  - [Notes/Constraints/Caveats (Optional)](#notesconstraintscaveats-optional)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Test Plan](#test-plan)
    - [Unit tests](#unit-tests)
    - [Golden tests](#golden-tests)
    - [e2e tests](#e2e-tests)
  - [Graduation Criteria](#graduation-criteria)
  - [Observability &amp; operational impact](#observability--operational-impact)
- [Implementation History](#implementation-history)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
<!-- /toc -->

## Summary

<!--
A good summary is at least a paragraph in length and should be written with a
wide audience in mind. It should be understandable by anyone who knows what
resource-state-metrics is, even if they are not familiar with the internals.
Focus on what the proposal does and why it matters, not how it is built.
-->

## Motivation

<!--
This section is for explicitly listing the motivation, goals, and non-goals of
this proposal. Describe why the change is important and the benefits to users.
-->

### Goals

<!--
List the specific goals of the proposal. What is it trying to achieve? How will
we know that this has succeeded?
-->

### Non-Goals

<!--
What is out of scope for this proposal? Listing non-goals helps to focus
discussion and make progress.
-->

## Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what you
are proposing, but should not include things like API designs or
implementation details — those live in "Design Details".
-->

### User Stories

<!--
Detail the things that people will be able to do if this proposal is
implemented. Include as much detail as possible so that people can understand
the "how" of the system. The goal here is to make this feel real for users
without getting bogged down.
-->

#### Story 1

#### Story 2

### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal? What are some important details that
didn't come across above? Go into as much detail as necessary here. This might
be a good place to talk about core concepts and how they relate.
-->

### Risks and Mitigations

<!--
What are the risks of this proposal, and how do we mitigate them? Think broadly:
for example, consider both security and how this will impact the larger
ecosystem. Consider including folks who also work outside the immediate area.
-->

## Design Details

<!--
This section should contain enough information that the specifics of your change
are understandable. This may include API specs (CRD fields, generated metric
shapes) though not necessarily a full description of the internals. Be specific
enough that it is clear how the feature interacts with the rest of
resource-state-metrics.
-->

### Test Plan

<!--
resource-state-metrics is validated primarily through golden-metrics tests. For
most proposals, describe the new or changed golden fixtures alongside unit and
end-to-end coverage.
-->

#### Unit tests

<!--
Which packages will gain or change unit coverage? Run with `make test_unit`.
-->

#### Golden tests

<!--
resource-state-metrics compares generated output against checked-in golden
fixtures under `tests/golden/`. Describe the fixtures you will add or update and
confirm `make compare_metrics` passes.
-->

#### e2e tests

<!--
Describe end-to-end coverage (`make test_e2e`) exercising this feature against a
running instance, including the resources and `ResourceMetricsMonitor`
configuration involved.
-->

### Graduation Criteria

<!--
How will we know that this proposal has been implemented and is working as
intended? Define the milestones that move it from provisional to implemented,
and any criteria for removing feature-gating or defaults changes. Keep this
scoped to resource-state-metrics releases; avoid Kubernetes-wide milestones.
-->

### Observability & operational impact

<!--
resource-state-metrics is an exporter, so the operational surface is mostly the
metrics it emits.

- Which metrics are added, changed, or removed? Include names and label sets.
- What is the cardinality impact? Estimate the number of series in realistic
  clusters and note any safeguards (allow/deny lists, resharding).
- How does an operator debug this feature when it misbehaves (logs, self
  metrics, configuration to inspect)?
-->

## Implementation History

<!--
Major milestones in the lifecycle of a proposal, for example:
- the date the idea was first discussed
- the date the proposal was merged as provisional
- the date the proposal was marked implementable
- the dates of related pull requests being merged
- the date the proposal was marked implemented
-->

## Drawbacks

<!--
Why should this proposal _not_ be implemented?
-->

## Alternatives

<!--
What other approaches did you consider, and why did you rule them out? These do
not need to be as detailed as the proposal, but should include enough
information to express the idea and why it was not acceptable.
-->

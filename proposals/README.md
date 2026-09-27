# resource-state-metrics proposals

This directory holds design proposals for resource-state-metrics (RSM). The process is adapted from the [Kubernetes Enhancement Proposal (KEP)](https://github.com/kubernetes/enhancements/tree/master/keps/NNNN-kep-template) template, trimmed to the parts that are useful for a metrics exporter.

A proposal is the unit of design review for a non-trivial change: a new (meta) metric family, a change to the `ResourceMetricsMonitor` API, a new configuration surface, or any change with cross-cutting impact. Small, obvious changes do not need a proposal — open a pull request directly.

<!-- toc -->
- [Lifecycle](#lifecycle)
- [Writing a proposal](#writing-a-proposal)
- [Layout](#layout)
<!-- /toc -->

## Lifecycle

A proposal moves through the following `Status` values:

| Status          | Meaning                                                      |
|-----------------|--------------------------------------------------------------|
| `provisional`   | Under discussion; the design may still change substantially. |
| `implementable` | Agreed on; ready to be built. Approved by the approvers.     |
| `implemented`   | The described change has shipped.                            |
| `rejected`      | Considered and declined.                                     |
| `withdrawn`     | Retracted by its authors.                                    |
| `replaced`      | Superseded by another proposal (see the `Replaces` field).   |

## Writing a proposal

1. Create the proposal directory by copying the template:

   ```console
   cp -r proposals/NNNN-proposal-template proposals/NNNN-my-short-title
   ```

   Copy [`NNNN-proposal-template/`](NNNN-proposal-template/README.md) to `proposals/NNNN-my-short-title/`. Rename `NNNN` to the tracking issue number (no leading-zero padding) once you have one.

2. Fill in the metadata table and the `Summary` and `Motivation` sections at a minimum, then open a pull request. Merge early and iterate.

3. Keep the table of contents in sync:

   ```console
   make proposals_toc
   ```

   `make lint_md` checks that the table of contents, prose style ([vale](https://vale.sh)), and formatting ([markdownfmt](https://github.com/Kunde21/markdownfmt)) are all up to date.

## Layout

```text
proposals/
├── README.md                     # this file
├── NNNN-proposal-template/       # the template, copied for each new proposal
│   └── README.md
└── NNNN-<short-title>/           # one directory per proposal
    ├── README.md                 # the proposal itself
    └── assets/                   # optional diagrams, images, etc.
```

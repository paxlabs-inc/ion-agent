# Ion Project Governance

This document describes how the Ion project is governed. It complements the
[Code of Conduct](CODE_OF_CONDUCT.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## Overview

Ion is an open-source project stewarded by [MatrixMCL](https://matrixmcl.com).
Development is transparent and driven by an authoritative, machine-readable
specification at [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Anyone may
contribute; a defined group of maintainers is responsible for the health,
direction, and quality bar of the project.

## Roles

### Contributors

Anyone who submits an issue, pull request, review, or documentation improvement
is a contributor. Contributors agree to the [Code of Conduct](CODE_OF_CONDUCT.md)
and the terms in [CONTRIBUTING.md](CONTRIBUTING.md).

### Reviewers

Reviewers are contributors with a track record of high-quality contributions in
a subsystem. They have review authority for the areas listed in
[CODEOWNERS](.github/CODEOWNERS) and their approval is required for changes in
those areas.

### Maintainers

Maintainers are responsible for the technical direction and release quality of
Ion. They:

- Set and enforce the engineering standards and acceptance bar.
- Own the release process and the specification.
- Merge pull requests once required reviews and CI gates pass.
- Add and remove reviewers and maintainers.

The current maintainer list is in [MAINTAINERS.md](MAINTAINERS.md).

## Decision making

The project favors **lazy consensus**: a proposal with no sustained objection
after reasonable notice is accepted. For substantive or cross-cutting changes,
the process is:

1. Open a [discussion](https://github.com/paxlabs-inc/ion-agent/discussions) or
   an issue describing the problem and proposed approach.
2. For architectural or acceptance-affecting changes, capture the decision as an
   Architecture Decision Record under [`docs/adr/`](docs/adr/) and update
   [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx) if requirements change.
3. Reviewers and maintainers weigh in. Where consensus cannot be reached, the
   maintainers make the final call.

The specification is authoritative. Generated summaries are informational
projections, not the source of truth.

## Adding maintainers and reviewers

A contributor may be nominated as a reviewer or maintainer by an existing
maintainer, based on sustained, high-quality contributions and good judgment.
Nominations are decided by consensus among the current maintainers.

## Changes to governance

Changes to this document follow the same decision-making process and require
maintainer consensus.

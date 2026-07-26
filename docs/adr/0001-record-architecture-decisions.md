# 0001. Record architecture decisions

- Status: Accepted
- Date: 2026-07-25
- Deciders: MatrixMCL Core Team

## Context

Ion's design carries decisions that are cross-cutting and hard to reverse: the
runtime owns authority, capabilities are gated behind acceptance boundaries, and
security decisions (SADRs) are binding. These decisions and their rationale need
a durable, reviewable home so that future contributors understand not just what
the system does, but why.

The authoritative plan lives in `spec/ion_spec/spec.kvx`, but the specification
captures requirements and tasks, not the narrative reasoning behind a design
choice.

## Decision

We will record significant architectural decisions as Architecture Decision
Records (ADRs) under `docs/adr/`, following the lightweight format popularized by
Michael Nygard. An ADR is created when a decision is cross-cutting, hard to
reverse, or affects acceptance boundaries. ADRs are reviewed through the normal
pull-request process described in [GOVERNANCE.md](../../GOVERNANCE.md).

## Consequences

- Contributors gain a searchable history of why the architecture is the way it
  is.
- Design discussions produce a durable artifact rather than living only in issue
  threads.
- ADRs that change requirements must be accompanied by an update to
  `spec/ion_spec/spec.kvx`; the specification remains authoritative where the two
  overlap.

## Alternatives considered

- **Keep design rationale in the wiki or issues.** Rejected: not versioned with
  the code and easily lost.
- **Put everything in the specification.** Rejected: the `.kvx` is a plan and
  acceptance ledger, not a place for narrative rationale.

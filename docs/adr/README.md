# Architecture Decision Records

This directory holds Architecture Decision Records (ADRs): short documents that
capture a significant architectural decision, its context, and its consequences.

We use ADRs for decisions that are cross-cutting, hard to reverse, or that affect
the acceptance boundaries in [`spec/ion_spec/spec.kvx`](../../spec/ion_spec/spec.kvx).
See [GOVERNANCE.md](../../GOVERNANCE.md).

## Format

Each ADR is a numbered Markdown file (`NNNN-title.md`) with:

- **Status** — Proposed, Accepted, Deprecated, or Superseded.
- **Context** — the forces at play.
- **Decision** — what we decided.
- **Consequences** — what becomes easier or harder as a result.

Use [`template.md`](template.md) as a starting point.

## Index

- [0001 — Record architecture decisions](0001-record-architecture-decisions.md)

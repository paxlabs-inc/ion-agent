# Roadmap

This is a human-readable summary. The **authoritative** plan, acceptance
criteria, and task status live in
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Where this document and the
specification disagree, the specification wins.

Ion is developed in dependency **waves**. A wave is a set of capabilities whose
acceptance criteria can be met once the prior waves are done. Work is task-driven
and one task is in progress at a time.

## Status legend

- **Done** — implemented and passing its acceptance criteria.
- **In progress** — active development path.
- **Planned** — accepted, not yet started.

## Themes

### Core runtime — Done / In progress

- Encrypted, actor-scoped session and memory storage (SQLite + envelope crypto).
- Provider-neutral model execution with ordered fallback and credential rotation.
- Policy, approval, idempotency, and audit boundaries for consequential tools.
- Durable work, scheduling, and recovery.
- Generated shared control-plane protocol consumed by every operator client.

### Web operator — In progress (primary path)

- Chat, approvals, sessions, providers, memory, security, and project
  projections.
- Computer and Software Studio projections behind acceptance boundaries.

### Terminal operator — In progress

- Embedded React Ink client for local attachment and supervised operation.

### Supervised subsystems — Planned / In progress

- Supervised Chromium browser under SSRF and private-network controls.
- Bounded specialist-agent registry and lifecycle.
- Rust HNSW vector-search sidecar (optional).

### Deployment & operations — In progress

- Docker image and local Compose stack.
- Kubernetes manifests, Helm chart, and systemd unit.
- Hardening guidance for TLS termination and network egress control.

## How priorities are set

Priorities follow the wave dependencies in the specification and the project's
[governance](GOVERNANCE.md) process. To propose a change, open a
[discussion](https://github.com/paxlabs-inc/ion-agent/discussions) or an issue.

## Non-goals

- Ion does not represent unavailable subsystems with invented data.
- Ion does not report success without authoritative outcome evidence.
- The development file KEK is not a production deployment mechanism.

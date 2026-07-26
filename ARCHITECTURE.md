# Ion architecture

Ion is one persistent agent runtime with two local operator clients. The Go
runtime owns authority; the web and terminal clients render the generated
control-plane protocol and do not invent subsystem state.

## Runtime shape

```text
Web operator ─┐
              ├─ authenticated control plane ─ agent runtime ─ providers
Terminal UI ──┘                │                    │
                               │                    ├─ policy-bound tools
                               │                    ├─ supervised projects/browser
                               │                    └─ bounded specialist agents
                               │
                               └─ encrypted session, memory, audit, and work state
```

The default deployment is a local `ion` process. Plain HTTP is limited to
loopback. A remote operator must terminate TLS in an operator-managed reverse
proxy.

## Repository map

```text
cmd/ion/                 CLI and runtime entry point
cmd/ion-web-e2e/         build-tagged browser acceptance helper
internal/agent/          provider/tool turn loop
internal/controlplane/   generated client contract and transports
internal/operatorapp/    production capability wiring and projections
internal/security/       vault, policy, sandbox, SSRF, and safety controls
internal/session/        encrypted durable session state
internal/memory/         journal, integrity, retrieval, and Cortex services
internal/project/        workspace, terminal, Git, runtime, and preview control
internal/browser/        supervised Chromium sessions
internal/swarm/          bounded specialist-agent registry and lifecycle
internal/tools/          policy-bound tool manager and lifecycle
internal/work/           durable work tracking
internal/scheduler/      durable scheduled work
ui/shared/               generated protocol types and client
ui/web/                  React web operator
ui/tui/                  embedded terminal operator
hnsw-service/            Rust vector-search sidecar
migrations/              embedded SQLite migrations
spec/ion_spec/           authoritative product specification
tests/                    integration, adversarial, and clean-install acceptance
```

## Authority and execution

1. An authenticated actor submits a control-plane request.
2. The operator application resolves the actor, session, channel, profile, and
   approval context.
3. Consequential operations pass through policy, approval, idempotency, and
   audit boundaries.
4. The runtime executes the real subsystem implementation.
5. Durable state and evidence are written before success is projected.
6. The clients render the resulting state, including explicit unavailable and
   partial outcomes.

Tool registration is not proof of availability. Runtime capability wiring is
the source of truth, and unavailable protocol operations remain unavailable
until a real implementation is registered.

## Data and security boundaries

- Vault keys come from a protected host key source. The file KEK is an explicit
  development-only fallback.
- Memory, sessions, approvals, work, and recovery state are actor-scoped.
- Browser and project runtimes are supervised local processes with bounded
  paths, ports, environments, and takeover leases.
- Network destinations pass through SSRF and private-network controls.
- Specialist agents receive scoped authority and do not inherit vault keys.
- Mutation results carry idempotency and audit evidence; generic accepted-only
  mutation responses are not used.

## Generated artifacts

The Go control-plane catalog generates `ui/shared/src/generated/protocol.ts`.
The web and terminal production artifacts are deterministic and embedded into
the Go binary. `docs/operator.md` and the spec projections are also generated
and checked for drift in CI.

## Technology

| Area | Technology |
|---|---|
| Runtime | Go 1.26.5 |
| Durable state | SQLite with encrypted application state |
| Integrity | BLAKE3, Merkle Mountain Range, and sparse Merkle tree |
| Web operator | React, TypeScript, and Vite |
| Terminal operator | React Ink |
| Vector search | Rust HNSW sidecar |
| Browser automation | Chromium via Chrome DevTools Protocol |

The acceptance criteria and task status live in
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Generated summaries are
informational projections, not the authoritative task ledger.

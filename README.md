<div align="center">

<img src="assets/ion_brand.png"/>

# Ion

**One persistent agent. Durable memory. Bounded execution. Visible evidence.**

Ion is an advanced general agent from [MatrixMCL](https://matrixmcl.com) — a single
persistent identity with encrypted memory, provider-neutral model execution, and
operator-controlled access to tools, projects, browsers, and specialist agents.

[![CI](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/ci.yml)
[![CodeQL](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml/badge.svg)](https://github.com/paxlabs-inc/ion-agent/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/paxlabs-inc/ion-agent.svg)](https://pkg.go.dev/github.com/paxlabs-inc/ion-agent)
[![Go Report Card](https://goreportcard.com/badge/github.com/paxlabs-inc/ion-agent)](https://goreportcard.com/report/github.com/paxlabs-inc/ion-agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-informational.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![OpenSSF Best Practices](https://img.shields.io/badge/OpenSSF-best%20practices-informational.svg)](https://www.bestpractices.dev/)

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/2MbBhn?referralCode=g2f45Y&utm_medium=integration&utm_source=template&utm_campaign=generic)

[Product](https://ion.matrixmcl.com) ·
[Documentation](docs/) ·
[Architecture](ARCHITECTURE.md) ·
[Security](SECURITY.md) ·
[Contributing](CONTRIBUTING.md) ·
[Roadmap](ROADMAP.md) ·
[Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)

**English** ·
[简体中文](README.zh-CN.md) ·
[Español](README.es.md) ·
[हिन्दी](README.hi.md) ·
[العربية](README.ar.md) ·
[Français](README.fr.md) ·
[Português](README.pt-BR.md)


---
</div>

> **Pre-release software.** The web operator and core runtime are the primary
> development path. The terminal client, supervised browser, Computer, and
> Software Studio remain subject to the acceptance boundaries recorded in
> [`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). Ion does not claim an
> operation succeeded unless the production path produced authoritative outcome
> evidence. Unavailable subsystems are projected as unavailable rather than
> represented by invented data.

## Table of contents

- [Why Ion](#why-ion)
- [Features](#features)
- [Architecture](#architecture)
- [Quick start](#quick-start)
  - [Run with Docker](#run-with-docker)
  - [Build from source](#build-from-source)
  - [Dev container](#dev-container)
- [Initialize](#initialize)
- [Run](#run)
- [Configuration](#configuration)
- [Deployment](#deployment)
- [Project layout](#project-layout)
- [Development](#development)
- [Testing and validation](#testing-and-validation)
- [Security](#security)
- [Roadmap](#roadmap)
- [Community and support](#community-and-support)
- [Contributing](#contributing)
- [License](#license)

## Why Ion

Most agent frameworks are libraries you assemble into a process that forgets
everything when it exits. Ion is the opposite: a **single durable runtime** that
owns identity, memory, policy, and evidence, and exposes that authority to thin
operator clients.

- **One identity, not many sessions.** Ion is one persistent actor with a
  continuous self-model, durable memory, and a stable audit trail — not a fresh
  context per request.
- **Authority lives in the runtime.** The Go runtime owns policy, approvals,
  idempotency, and audit. The web and terminal clients render a generated
  control-plane protocol and never invent subsystem state.
- **Evidence over optimism.** A consequential operation is only reported as
  successful once the real subsystem produced authoritative outcome evidence and
  durable state was written. Unavailable capabilities are shown as unavailable.
- **Provider-neutral by design.** Model execution is abstracted behind a
  provider layer with explicit, ordered fallback and credential rotation.
- **Bounded blast radius.** Specialist sub-agents receive scoped authority and
  never inherit vault keys. Browser and project runtimes are supervised local
  processes with bounded paths, ports, environments, and takeover leases.

## Features

| Capability | Description |
|---|---|
| **Encrypted memory & sessions** | Actor-scoped, AES-256-GCM envelope encryption with a KEK → User Key → per-object DEK hierarchy, atomic rotation, and key zeroization on shutdown. |
| **Durable session store** | Pure-Go SQLite with WAL, single-writer queue, reader pool, versioned embedded migrations, and compression-triggered child sessions. |
| **Provider-neutral execution** | A provider-agnostic request/generation/tool-call/stream model with validated wire adapters and ordered fallback on rate limits and failures. |
| **Policy, approval & audit** | Consequential tools pass through policy, human approval, idempotency, and audit boundaries. No generic accepted-only mutation responses. |
| **Durable work & scheduling** | Work tracking, scheduling, and recovery survive restarts; task lifecycle is decoupled from operator connectivity. |
| **Bounded specialist agents** | A registry of scoped sub-agents with bounded lifecycle that never inherit vault keys. |
| **Supervised browser** | Chromium sessions driven over the Chrome DevTools Protocol under SSRF and private-network controls. |
| **Vector retrieval** | An optional Rust HNSW sidecar for high-recall similarity search. |
| **Web operator** | A React operator with chat, approvals, sessions, providers, memory, security, projects, Computer, and Software Studio projections. |
| **Terminal operator** | An embedded React Ink terminal client for local attachment and supervised operation. |
| **Generated protocol** | A single Go control-plane catalog generates the shared TypeScript client used by every operator; drift is rejected in CI. |

## Architecture

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

The default deployment is a single local `ion` process. Plain HTTP is limited to
loopback; remote access requires an operator-managed TLS reverse proxy.

Authority and execution follow one path:

1. An authenticated actor submits a control-plane request.
2. The operator application resolves actor, session, channel, profile, and
   approval context.
3. Consequential operations pass through policy, approval, idempotency, and audit.
4. The runtime executes the real subsystem implementation.
5. Durable state and evidence are written **before** success is projected.
6. Clients render the resulting state, including explicit unavailable and partial
   outcomes.

For the full design, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Quick start

### Run with Docker

The fastest way to try Ion locally. The operator is loopback-only by default.

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

# Build the complete appliance and start its single ingress on port 8080
docker compose -f docker/docker-compose.yml up --build
```

Open <http://127.0.0.1:8080>. The one image contains Ion, Ion Computer,
Chromium, and ONLYOFFICE. See [docker/README.md](docker/README.md) for its
volume and environment variables.

### Build from source

**Requirements**

| Tool | Version |
|---|---|
| Go | 1.26.5 |
| Node.js | 22.22+ (Node 22 line) |
| npm | 11 |
| Rust | 1.78.0 (optional HNSW service) |
| Chromium | for native browser acceptance tests |

```bash
git clone https://github.com/paxlabs-inc/ion-agent.git
cd ion-agent

make build
```

The release build embeds deterministic web and terminal artifacts into `bin/ion`.

### Dev container

A ready-to-code environment with Go, Node, Rust, and Chromium preinstalled is
provided under [`.devcontainer/`](.devcontainer/). In VS Code, run
**Dev Containers: Reopen in Container**, or use the GitHub Codespaces button.
Everything from `make build` to `make ci` works out of the box.

## Initialize

Production initialization uses the host's protected key source:

```bash
./bin/ion init
```

Headless development environments without a supported protected key source may
explicitly opt into the development-only file KEK:

```bash
./bin/ion init --dev-file-kek
```

> The development fallback must **not** be used as a production deployment
> mechanism.

## Run

```bash
# Web operator (http://127.0.0.1:4174 by default)
./bin/ion dashboard

# Terminal operator with a supervised local runtime
./bin/ion tui

# Attach the terminal operator to an existing dashboard runtime
./bin/ion tui --attach

# Print version, commit, and build metadata
./bin/ion version
```

Plain HTTP is loopback-only. Remote access requires an operator-managed TLS
reverse proxy.

## Configuration

Ion reads its data directory, listen address, and key source from flags and
environment variables. The most common knobs:

| Flag / Env | Default | Description |
|---|---|---|
| `--data-dir` / `ION_DATA_DIR` | `~/.ion` | Durable data directory (SQLite, vault, work state). |
| `--listen` / `ION_WEB_LISTEN` | `127.0.0.1:4174` | Web operator listen address. Bind to loopback only. |
| `--origin` / `ION_WEB_ORIGIN` | listen URL | Exact public browser origin for remote TLS deployments. |
| `ION_AUTH_USERNAME` + one password variable | unset | Required operator login for remote and Railway deployments. |
| `ION_VAULT_KEK` | unset | Base64-encoded 32-byte vault KEK for the Railway appliance. |
| `--dev-file-kek` | off | Development-only file KEK. Never use in production. |

See [docs/configuration.md](docs/configuration.md) for the complete reference.

## Deployment

Ion ships production-oriented deployment assets under [`deploy/`](deploy/):

- **Docker Compose** — [`deploy/compose/`](deploy/compose/) for a single-host
  operator behind a TLS reverse proxy.
- **Kubernetes** — [`deploy/kubernetes/`](deploy/kubernetes/) manifests
  (namespace, deployment, service, config, ingress) with a Kustomize base.
- **Helm** — [`deploy/helm/ion/`](deploy/helm/ion/) chart for parameterized
  installs.
- **Railway** — the root [`Dockerfile`](Dockerfile) and
  [`railway.toml`](railway.toml) run the complete appliance as one service.
- **systemd** — [`deploy/systemd/`](deploy/systemd/) unit for bare-metal hosts.

Read [docs/deployment.md](docs/deployment.md) and [deploy/README.md](deploy/README.md)
before exposing Ion beyond loopback. TLS termination, key source, and network
egress controls are operator responsibilities.

## Project layout

```text
cmd/ion/                 CLI and runtime entry point
cmd/ion-appliance/       single-service container supervisor and ingress
cmd/ion-computer/        private graphical Computer host
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
deploy/                  Kubernetes, Helm, Compose, and systemd assets
docker/                  container image and local Compose stack
tests/                   integration, adversarial, and clean-install acceptance
```

## Development

```bash
make build          # Build the operator release (web + TUI + Go binary)
make build-all      # Also build the Rust HNSW sidecar
make run            # Build and run the web operator
make dev            # Auto-rebuild on change (air)
make fmt tidy       # Format and tidy
make help           # List every target
```

House rules worth knowing before you open a PR:

- Deliver complete, runnable artifacts — not diffs.
- No stubs, mocks, or fakes except at true external boundaries.
- Client UI separates layers by background-color contrast, never border strokes.
- No emojis, purple gradients, or glow effects in UI or output.

See [CONTRIBUTING.md](CONTRIBUTING.md) and
[`spec/ion_spec/ENGINEERING_STANDARDS.md`](spec/ion_spec/ENGINEERING_STANDARDS.md).

## Testing and validation

```bash
make test-unit        # Unit tests with the race detector
make vet              # go vet
make verify-deps      # Verify checksummed Go and Rust dependencies
make test-operator    # Shared, web, TUI, browser, accessibility, and budget gates
make spec-validate    # Validate the authoritative spec.kvx
make ci               # Full CI pipeline
```

CI runs the same gates on every push and pull request across Go, Rust, and the
operator clients, and rejects generated-contract and documentation drift. See
[`.github/workflows/`](.github/workflows/).

<div align="center">

## Security

Ion is a continuous-presence agent with autonomous action capability, and its
security model is treated as a first-class product surface: eight adversary
classes, defined crown-jewel assets, and binding security architecture decisions
(SADRs). Sub-agents never inherit vault keys, idle-time principals cannot execute
high-risk or external operations, and all safety overrides are logged and
user-visible.

**Do not report vulnerabilities in public issues.** Use
[GitHub private vulnerability reporting](https://github.com/paxlabs-inc/ion-agent/security/advisories/new)
and follow the process in [SECURITY.md](SECURITY.md).

## Roadmap

The authoritative plan and task status live in
[`spec/ion_spec/spec.kvx`](spec/ion_spec/spec.kvx). A human-readable summary is
maintained in [ROADMAP.md](ROADMAP.md). Generated summaries are informational
projections, not the authoritative task ledger.

## Community and support

- **Questions & ideas** — [GitHub Discussions](https://github.com/paxlabs-inc/ion-agent/discussions)
- **Bugs & features** — [GitHub Issues](https://github.com/paxlabs-inc/ion-agent/issues)
- **How to get help** — [SUPPORT.md](SUPPORT.md)
- **Community standards** — [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **Project governance** — [GOVERNANCE.md](GOVERNANCE.md)

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), pick up an
issue, and open a focused pull request. All contributors are expected to follow
the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Ion is free and open-source software licensed under the [MIT License](LICENSE).


Copyright © 2026 MatrixMCL — <a href="https://ion.matrixmcl.com">ion.matrixmcl.com</a>

</div>

---
name: matrix-core-architecture
description: "Navigate and extend the Matrix-core codebase — subsystem map, directory structure, key interfaces, Merkle/cortex/identity architecture, .kvx codegraph files."
version: 1.0.0
author: Neo
license: MIT
platforms: [linux]
metadata:
  ion:
    tags: [matrix, paxlabs, architecture, go, codebase, cortex, merkle]
    related_skills: [technical-architecture-proposal, repository-deep-research]
---

# Matrix-Core Architecture

Navigate, understand, and extend the Matrix-core codebase (Paxlabs). This is a Go monorepo at `/data/matrix-core/` containing the full Matrix agent platform: agent runtime, cortex memory system, MCL language, codegraph, executor, and supporting services.

## When to Use

- Working on any Matrix-core feature, bug, or extension
- Need to find where a capability lives in the codebase
- Adding new subsystems or extending existing ones
- Understanding how components interact (merkle, cortex, identity, lifecycle)
- Navigating .kvx codegraph files (see pitfall below)

## Directory Structure

```
matrix-core/
├── MCL/                    # Matrix Control Language (lexer, parser, IR, envelope, LLM)
│   ├── cmd/mclc/           # MCL compiler CLI
│   ├── envelope/           # Message envelope system (kind, intent, body)
│   ├── ir/                 # Intermediate representation (plan, intent, encode)
│   ├── llm/                # LLM client + identity preamble injection
│   └── mtx/                # Matrix TX language (lexer, parser, AST, interpreter, validator)
├── bridge/                 # MCL-Cortex bridge (bundles, args)
├── cassandra/              # Adjudicator system (priors, prompts, verdicts)
├── chronos/                # Scheduler daemon (alarms, heartbeats, wake, dispatch)
├── codegraph/              # Code analysis + Merkle tree
│   ├── cmd/codegraph/      # Codegraph CLI
│   ├── enrich/             # Enrichment (embed, summarize, salience, cody)
│   ├── extract/            # Source extraction (calls, types, files, incremental)
│   ├── merkle/             # BLAKE3 Merkle tree over repo files
│   ├── model/              # Data model (Index, Digest, ID)
│   ├── retrieve/           # Query/retrieval API (diff, symbol lookup)
│   ├── selfmodel/          # Self-description artifact (Summary, Merkle, Scope)
│   └── store/              # Codegraph persistence
├── construct/              # Schema-driven construction (primitives, surfaces, transport)
├── cortex/                 # Memory + state system (THE core)
│   ├── cmd/                # cortex-shell, embed-smoke, two-model-smoke
│   ├── cortex.go           # Main cortex orchestration
│   ├── embed/              # Embedding pipeline (API embedder, chain)
│   ├── forms/              # Memory forms
│   ├── journal/            # Append-only journal
│   ├── keys/               # Pebble key encoding (all namespaces)
│   ├── memory/             # Memory types, codec, validation
│   ├── query/              # Graph query (eval, find, predicate)
│   ├── replay/             # State replay (drop, rebuild)
│   ├── salience/           # Salience scoring
│   ├── scope/              # Scope matching + verification
│   ├── snapshot/           # MMR + SMT → SnapshotManifest (OverallRoot)
│   ├── store/              # Pebble store + write batch
│   └── vector/             # Vector storage
├── cody/                   # Coding agent (orchestrator, worker, sandbox, tools)
├── construct/              # Schema system (primitives, surfaces, transport)
├── deus/                   # Platform daemon (hosting, gateway, discovery, metering)
├── executor/               # Runtime execution engine
│   ├── cmd/mcl-execute/    # Main executor daemon (pipeline, tools, cortex, daemon)
│   ├── compilecache/       # Compile cache (sha256 key → cached compile)
│   ├── lifecycle/          # Intent lifecycle state machine
│   ├── mcp/                # MCP client (stdio, HTTP, JSON-RPC)
│   ├── runtime/            # Runtime (skill loader, walker, coercion)
│   └── tool/               # Tool registry + manifest
├── gateway/                # Matrix gateway (auth, ledger, proxy, routing)
├── layerx/                 # Layer-X settlement (chain, deposit, ledger, events)
├── neo/                    # Neo agent (THE agent runtime)
│   ├── cmd/neo/            # Neo CLI (serve)
│   ├── internal/agent/     # Agent loop (identity, heartbeat, prompt, compaction)
│   ├── internal/config/    # Configuration
│   ├── internal/conversation/ # Conversation store
│   ├── internal/delegate/  # Sub-agent delegation
│   ├── internal/llm/       # LLM client
│   ├── internal/memory/    # Memory management (classifier, embedder, pattern)
│   ├── internal/server/    # HTTP server (routes, SSE, queue, swarm)
│   └── internal/tools/     # Tool definitions
└── router/                 # Matrix router (proxy, provisioning, JWT, routing)
```

## Key Subsystems

### 1. Merkle Tree (codegraph/merkle/)

BLAKE3-based content-addressed Merkle tree over the repo. Every file is a leaf (hash over LF-normalized content). Directories are internal nodes (hash over sorted child entries). Root hash commits to entire codebase state.

**Domain separation:** `matrix.codegraph.merkle.leaf.v1\0` for leaves, `matrix.codegraph.merkle.node.v1\0` for nodes.

**Key types:** `Tree` (immutable after construction), `Changes` (diff result with Added/Removed/Changed).

**Entry points:** `FromLeaves()`, `FromContentMap()`, `Diff(old, new)`.

### 2. Self-Model (codegraph/selfmodel/)

`Artifact` struct with `Summary`, `Merkle`, `Scope` fields. `Distill()` creates a self-description that includes the merkle root and scope. The machine describes itself with its codebase hash attached.

### 3. Snapshot System (cortex/snapshot/)

Cryptographic state commitments using MMR (Merkle Mountain Range) for the journal and SMT (Sparse Merkle Tree) per namespace ("memories", "edges").

**Manifest** contains: `SchemaVersion`, `Actor`, `JournalRoot`, `StateRoots`, `OverallRoot` (SHA-256 over journal + state roots), `SignedBy`, `Signature`, `Counters`.

**`ComputeOverallRoot()`** is the canonical composition: `sha256(domain || schema || journalRoot || nsCount || sorted(ns + stateRoot))`.

### 4. Identity System (MCL/llm/identity.go + neo/internal/agent/identity.go)

- `IdentityPreamble` injected as first system message in every LLM call
- `IdentityVersion` ("matrix-identity-v1") mixed into compile-cache keys
- `scrubIdentity()` rewrites self-identification as underlying model to agent name
- `identityReanchorNudge()` corrects models that break character

### 5. Lifecycle Machine (executor/lifecycle/)

Per-intent state machine. States: Drafting → Proposed → Accepted → Executing → Completed/Failed/Cancelled. Transitions driven by MCL envelope kinds. Full event history with materiality classification.

### 6. Cortex Keys (cortex/keys/)

Hierarchical Pebble key structure. Namespaces: `m/` (memory head), `mv/` (memory versions), `e/from/` + `e/to/` (edges), `j/` (journal), `snap/` (snapshots), `idx/type/`, `idx/tag/`, `salience/`, `vec/meta/`, `meta/` (metadata), `accum/` (MMR), `idx/smt/` (SMT nodes).

All numeric components are big-endian fixed-width binary. IDs are 16-byte ULIDs.

## .kvx Files (graph/ directory)

The `graph/` directory at the repo root contains `.kvx` files. These are **generated codegraph output**, not source code. They contain:

- Node definitions with BLAKE3 digests
- Edge relationships (imports, calls, contains, references)
- File ranges and byte ranges

**Critical pitfall:** To read the actual source code, navigate to the parent directory. The .kvx file at `graph/matrix/codegraph/selfmodel.kvx` describes the package; the real code is at `codegraph/selfmodel/selfmodel.go`.

## Extension Patterns

When adding a new capability to Matrix-core:

1. **New namespace in cortex:** Add prefix to `cortex/keys/keys.go`, create store methods, anchor into snapshot via `StageXUpdate()`.
2. **New envelope kind:** Add to `MCL/envelope/kinds.go`, add transition to `executor/lifecycle/machine.go`.
3. **New tool for Neo:** Add to `neo/internal/tools/tools.go`, implement handler in `executor/cmd/mcl-execute/`.
4. **New codegraph enrichment:** Add to `codegraph/enrich/`, wire into `cmd/codegraph/main.go`.

## Pitfalls

- **.kvx files are generated output, not source.** Always navigate UP to find the actual .go files.
- **Don't modify graph/ .kvx files.** They're regenerated by the codegraph tool.
- **Snapshot OverallRoot is deterministic.** Changing the `ComputeOverallRoot()` algorithm requires a `SchemaVersion` bump.
- **Identity preamble changes require `IdentityVersion` bump** or compile-cache entries go stale.
- **Cortex keys use fixed-width big-endian encoding.** Don't introduce variable-width fields without careful namespace design.
- **The merkle tree domain-separates leaf and node hashes.** Never mix them.

## Reference

- `references/subsystem-map.md` — Detailed file-by-file map of key interfaces and entry points.

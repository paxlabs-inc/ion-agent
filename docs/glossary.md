# Glossary

Terms used throughout Ion's code and documentation.

**Actor** — The authenticated principal a control-plane request runs as. Memory,
sessions, approvals, work, and recovery state are actor-scoped.

**Audit evidence** — Durable, verifiable record written for consequential
operations. Ion does not report success without authoritative outcome evidence.

**Control plane** — The authenticated protocol between the operator clients and
the runtime. A single Go catalog generates the shared TypeScript client.

**Cortex** — Ion's persistent memory subsystem (journal, integrity, retrieval).

**DEK (Data Encryption Key)** — Per-object key in the encryption hierarchy
(KEK → User Key → per-object DEK).

**HNSW** — Hierarchical Navigable Small World graph; the algorithm behind the
optional Rust vector-search sidecar.

**Idempotency** — Property ensuring a repeated mutation does not cause a repeated
effect. Mutation results carry idempotency and audit evidence rather than generic
accepted-only responses.

**KEK (Key Encryption Key)** — Top of the key hierarchy, derived from a protected
host key source. The development file KEK is an explicit development-only
fallback.

**Operator** — A thin client (web or terminal) that renders control-plane state.
Operators do not invent subsystem state.

**Policy pipeline** — The path consequential tools pass through: policy,
approval, idempotency, and audit.

**Sidecar** — A companion process. Ion uses a Rust HNSW sidecar for vector
search and a reverse-proxy sidecar for TLS in deployments.

**Specialist agent (sub-agent)** — A bounded agent with scoped authority. Never
inherits vault keys.

**SSRF controls** — Server-Side Request Forgery protections applied to network
destinations, including private-network controls.

**Supervised runtime** — A local process (browser or project runtime) with
bounded paths, ports, environments, and takeover leases.

**Unavailable projection** — How Ion represents a subsystem it cannot back with a
real implementation: as unavailable, not as invented data.

**Vault** — The encrypted store for all sensitive application state.

**Wave** — A dependency-ordered set of capabilities in the specification. Work is
task-driven within waves.

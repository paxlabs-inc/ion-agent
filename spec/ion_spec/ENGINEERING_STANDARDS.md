# ION ENGINEERING STANDARDS — DEFINITIVE SPECIFICATION

> **Status**: MANDATORY — No code, test, documentation, or configuration may be merged without satisfying every applicable section.
> **Authority**: This document supersedes all prior architecture documents where they conflict.
> **Version**: 1.0.0 | **Date**: 2026-07-18
> **Inputs**: genesis-paper-part1-4.md, ion-bible.md, ion-phase2.5-revision.md, ion-implementation-plan.md

---

## 0. FOUNDATIONAL PRINCIPLES

### 0.1 The Five Laws

Every line of code, every test, every document, every configuration file in the Ion project MUST satisfy these five laws. Violation of any law is a blocking defect.

**LAW 1: HONESTY OVER COMPLETION.**
Never fabricate success. Never mark a task done that is not done. Never produce a test that passes by substituting a fake for a real code path. Never generate plausible-looking output when a tool, install, or network call fails. Report blockers honestly. Surface partial results as partial. `ErrIncomplete` is the model for every component — when you cannot finish, say so, say where you got stuck, and say what you tried.

**LAW 2: SECURITY IS NOT A FEATURE — IT IS THE FOUNDATION.**
Every component is an attack surface. Every data flow is a potential exfiltration path. Every autonomous action is a potential liability. Security is not reviewed after implementation — it is designed before the first line of code is written. The Bible is not advisory — it is binding. The Day Zero checklist gates Phase 2. The Day 100 checklist gates Phase 6. No exceptions.

**LAW 3: TESTS ARE PROOF, NOT RITUAL.**
A test that does not exercise a real code path is a lie. A test that passes because the mock returns what the test expects is worse than no test — it is false confidence. Every test in this project must exercise real code: real agent loops, real tool dispatch, real memory operations, real encryption, real policy evaluation. Stubs, mocks, and fakes are prohibited except at true external boundaries (LLM provider responses, network I/O) where they must be explicitly documented as such.

**LAW 4: DOCUMENTATION IS DERIVATIVE, NOT SPECULATIVE.**
Documentation must be generated from or derived from the code it describes. Hand-written documentation that describes behavior the code does not implement is a bug. The self-model is derived from the codegraph, not hand-written. The capability surface is generated from the self-model artifact. The spec is the source of truth; the code implements the spec; the tests prove the code; the documentation reflects the code.

**LAW 5: THE AGENT MUST BE ALIVE, NOT PERFORMING.**
Liveness is not theater. Emotional state modulates decision-making — it does not generate sentiment. Curiosity drives real exploration — it does not produce simulated wonder. Dreams reorganize real memories — they do not generate poetic text. Every liveness mechanism must produce observable, measurable behavioral changes in the agent's execution — not cosmetic output variations.

---

## 1. LANGUAGE AND TOOLCHAIN STANDARDS

### 1.1 Language Versions (Hard Floor — No Exceptions)

| Language | Minimum Version | Required Features | Enforcement |
|----------|----------------|-------------------|-------------|
| **Go** | 1.22.0 | `range-over-func`, modern `net/http` routing, `GOMEMLIMIT` | `go.mod` declares `go 1.22`; CI rejects lower |
| **Rust** | 1.78.0 | `async fn` in traits, `usearch` crate compatibility | `rust-toolchain.toml` pins 1.78+; CI validates |
| **TypeScript** | 5.4.0 | `const` type parameters, improved narrowing | `tsconfig.json` sets `strict: true`; CI type-checks |
| **Python** | 3.11.0 | Exception groups, `tomllib`, `TaskGroup` | Only for tool scripts + test harnesses; never in production binary |

### 1.2 Build Standards

```
STANDARD-BUILD-001: Every Go module MUST build with CGO_ENABLED=0.
  Rationale: Pure-Go deployment. No C compiler in the build pipeline.
  Verification: CI runs `CGO_ENABLED=0 go build ./...` on every commit.

STANDARD-BUILD-002: The production artifact MUST be a single binary.
  Rationale: Eliminates DLL hell, simplifies deployment, enables embedding.
  Includes: Embedded dashboard (TypeScript via esbuild + embed.FS),
            embedded SQLite schema migrations,
            embedded default SOUL.md template.
  Verification: `ldd ion` returns "not a dynamic executable" on Linux.

STANDARD-BUILD-003: All Go dependencies MUST be pure-Go or have a documented fallback.
  Required: modernc.org/sqlite (pure Go), zeebo/blake3 (pure Go), decred/secp256k1 (pure Go)
  Prohibited: mattn/go-sqlite3 (CGo), ethereum/go-ethereum/crypto (CGo)
  Verification: `go mod graph | grep -E 'cgo|sqlite3'` returns empty.

STANDARD-BUILD-004: Reproducible builds.
  go.mod, go.sum, Cargo.toml, Cargo.lock, package.json, package-lock.json
  ALL committed. No `go get` during CI. No `cargo build` without lockfile.
  Verification: `go mod tidy && git diff --exit go.mod go.sum` in CI.
```

### 1.3 Code Quality Standards

```
STANDARD-QUALITY-001: Zero lint warnings.
  Tool: golangci-lint (Go), clippy (Rust), eslint (TypeScript)
  Config: .golangci.yml, clippy.toml, .eslintrc.yml committed to repo root.
  CI: Lint runs on every commit. Any warning = build failure.
  No suppression comments without a linked issue number.

STANDARD-QUALITY-002: Cyclomatic complexity ≤ 15 per function.
  Rationale: Functions above 15 are unmaintainable and untestable.
  Tool: gocyclo (Go), cognitive_complexity (Rust)
  CI: Rejects functions above threshold. No exceptions.

STANDARD-QUALITY-003: No naked returns. No named return values longer than 3.
  Rationale: Named returns obscure control flow. Naked returns hide mutation.
  Enforcement: linter rule.

STANDARD-QUALITY-004: Error handling.
  Every error MUST be either:
  (a) Handled (checked, logged, or propagated), or
  (b) Explicitly discarded with a comment explaining why (e.g., // best-effort cleanup).
  `_ = err` without a comment = build failure.
  Tool: errcheck (Go), deny(unused_must_use) (Rust)

STANDARD-QUALITY-005: No panic in library code.
  panic() is prohibited in all packages except:
  - main() during startup (fatal configuration errors)
  - Test helpers (t.Fatal equivalent)
  All other code MUST return errors. Recover() is prohibited except
  in the top-level agent loop crash handler (which logs and restarts).

STANDARD-QUALITY-006: Context propagation.
  Every function that performs I/O, acquires a lock, or calls a provider
  MUST accept `context.Context` as its first parameter.
  Every goroutine MUST select on `ctx.Done()` for cancellation.
  context.Background() is prohibited outside main() and test setup.
  TODO context is a build failure.

STANDARD-QUALITY-007: Resource cleanup.
  Every resource (file handle, database connection, goroutine, channel)
  MUST have a documented ownership model and cleanup path.
  defer cleanup() immediately after resource acquisition.
  Leaked goroutines are a blocking defect (detected by runtime.NumGoroutine
  growth in tests).
```

---

## 2. ARCHITECTURE STANDARDS

### 2.1 Module Structure

```
ion/
├── cmd/
│   └── ion/          # Main binary entry point
│       └── main.go          # Wiring only — no business logic
├── internal/
│   ├── agent/               # Core agent loop (AgentLoop, Turn)
│   ├── perception/          # Activation composer, prefetch, pager, emotional state
│   ├── belief/              # Premise ledger, memory taxonomy, self-model, confidence monitor
│   ├── intent/              # Task graph, prediction dispatch, goal generator, orchestrator
│   ├── action/              # Dual-rail executor, error recovery, tool transparency
│   ├── reflection/          # Cassandra, prediction reconciler, skill forge, writeback
│   ├── presence/            # Heartbeat, morning brief, Automatrix, temporal embodiment
│   ├── memory/              # Cortex, journal, MMR, SMT, HNSW client, decay
│   ├── security/            # Vault, policy pipeline, SSRF dispatcher, circuit breakers
│   ├── provider/            # O1 protocol, provider adapters, credential pool
│   ├── tools/               # Tool registry, execution, MCP server/client
│   ├── session/             # SQLite session store, FTS5 search, compression
│   ├── liveness/            # Curiosity engine, Dreamweaver, social awareness, aesthetics
│   ├── subagent/            # Spawning, closed-verb protocol, orphan recovery, lanes
│   └── config/              # Configuration, constants, defaults
├── pkg/
│   ├── types/               # Shared types (no business logic, no state)
│   └── protocol/            # O1 protocol types, closed-verb types
├── dashboard/               # TypeScript/React safety dashboard
│   ├── src/
│   ├── package.json
│   └── tsconfig.json
├── hnsw-service/            # Rust HNSW microservice
│   ├── src/
│   ├── Cargo.toml
│   └── Cargo.lock
├── tools/                   # Python tool implementations (out-of-process)
├── migrations/              # SQLite schema migrations (versioned, idempotent)
├── specs/                   # This file + spec.kvx files
├── tests/
│   ├── integration/
│   ├── adversarial/
│   ├── performance/
│   └── chaos/
├── .golangci.yml
├── go.mod
├── go.sum
└── Makefile
```

### 2.2 Dependency Rules (Strict Layering)

```
STANDARD-ARCH-001: Unidirectional dependency graph.
  cmd/ → internal/ → pkg/
  internal/ packages MAY import from pkg/ but NEVER from cmd/.
  internal/ packages MUST NOT create import cycles.
  Enforcement: `go vet ./...` + custom cycle detector in CI.

STANDARD-ARCH-002: Package boundaries are API boundaries.
  Every exported symbol in internal/ is an API contract.
  Changing an exported signature requires updating all consumers
  in the same commit (no broken intermediate states).

STANDARD-ARCH-003: No shared mutable state across packages.
  Packages communicate via:
  (a) Function calls (synchronous)
  (b) Channels (asynchronous)
  (c) Interfaces (dependency injection)
  NEVER via global variables, init() side effects, or sync.Pool
  shared across package boundaries.

STANDARD-ARCH-004: Every internal/ package has a single responsibility.
  agent/ orchestrates the turn loop — it does NOT implement tools, memory, or encryption.
  memory/ manages the Cortex — it does NOT implement the agent loop or tools.
  If a function doesn't fit the package's documented responsibility, it belongs elsewhere.
```

### 2.3 Interface Standards

```
STANDARD-IFACE-001: Interfaces are defined by the consumer, not the producer.
  The session/ package defines what it needs from memory/. The agent/ package
  defines what it needs from provider/. This prevents interface bloat and
  ensures interfaces carry only what the consumer actually uses.

STANDARD-IFACE-002: Interface width ≤ 5 methods.
  Interfaces wider than 5 methods are a design smell. Split them.
  Exception: The Cortex interface (store/update/archive/search/HNSW/search/recent/merkle)
  is 8 methods — it is the single approved exception, documented as such.

STANDARD-IFACE-003: Accept interfaces, return structs.
  Functions accept narrow interfaces and return concrete types.
  Returning an interface hides the concrete type's capabilities
  and forces callers to type-assert.

STANDARD-IFACE-004: No empty interfaces.
  `interface{}` / `any` is prohibited except in:
  - Generic serialization (JSON marshal/unmarshal)
  - Test assertion helpers
  All other uses require a type-safe alternative.
```

---

## 3. DATA STRUCTURE STANDARDS

### 3.1 Type Safety

```
STANDARD-DATA-001: No primitive obsession.
  Memory types MUST be `MemoryType` (byte alias with constants), not raw `byte`.
  Premise statuses MUST be `PremiseStatus` (string alias with constants), not raw `string`.
  Tool failure classes MUST be `FailureClass` (string alias with constants), not raw `string`.
  Rationale: Type aliases prevent passing a MemoryType where a FailureClass is expected.
  Enforcement: Custom linter rule checks for raw byte/string in struct fields
  where a typed alias exists.

STANDARD-DATA-002: UUIDs for all entity identifiers.
  Every entity (Premise, ToolEvent, Memory, Session, SubAgent, CuriosityTarget)
  uses `uuid.UUID` (v4) as its primary identifier.
  String-based IDs are prohibited. Incrementing integer IDs are prohibited
  (they leak count, create contention, and enable enumeration attacks).

STANDARD-DATA-003: Timestamps are `time.Time`, never integers or strings.
  All timestamps use `time.Time` internally. Serialization uses RFC3339Nano.
  Storage uses Unix microseconds (int64) for SQLite compatibility.
  time.Now() is prohibited in business logic — use a Clock interface
  for testability (injectable via dependency injection).

STANDARD-DATA-004: Immutable after construction.
  Structs that represent completed events (ToolEvent, PredictionRecord, JournalEntry)
  MUST be immutable after creation. Use unexported fields with getter methods,
  or construct-only factories. Mutation of historical records is a blocking defect.

STANDARD-DATA-005: Validation at construction.
  Every struct with business invariants MUST validate them at construction time.
  Invalid states MUST be unrepresentable. Use constructor functions (NewXxx)
  that return errors for invalid inputs. Zero-value structs that violate
  invariants are a design failure.
```

### 3.2 Core Type Definitions

All types MUST match these exact definitions. Field names, types, and JSON tags are canonical.

```go
// === PREMISE ===
type PremiseStatus string
const (
    PremiseAssumption PremiseStatus = "assumption"
    PremiseCited      PremiseStatus = "cited"
    PremiseRefuted    PremiseStatus = "refuted"
    PremiseSuspicious PremiseStatus = "suspicious"
)

type PremiseSource string
const (
    SourceSelfModel  PremiseSource = "self-model"
    SourceCortex     PremiseSource = "cortex"
    SourceToolEvidence PremiseSource = "tool-evidence"
    SourceUser       PremiseSource = "user"
    SourceInference  PremiseSource = "inference"
)

type Citation struct {
    ToolEventID   uuid.UUID `json:"tool_event_id"`
    MMRLeafHash   [32]byte  `json:"mmr_leaf_hash"`
    MMRRootAtTime [32]byte  `json:"mmr_root_at_time"`
    Verified      bool      `json:"verified"`
}

type Premise struct {
    ID        uuid.UUID     `json:"id"`
    Statement string        `json:"statement"`
    Status    PremiseStatus `json:"status"`
    Source    PremiseSource `json:"source"`
    Citation  *Citation     `json:"citation,omitempty"`
    Load      float64       `json:"load"`       // 0.0-1.0
    CreatedAt time.Time     `json:"created_at"`
    RefutedAt *time.Time    `json:"refuted_at,omitempty"`
    PlanID    uuid.UUID     `json:"plan_id"`
    StepIndex int           `json:"step_index"`
}

// === TOOL EVENT ===
type FailureClass string
const (
    FailureNone      FailureClass = ""
    FailureTimeout   FailureClass = "timeout"
    FailureAuth      FailureClass = "auth"
    FailureRateLimit FailureClass = "rate_limit"
    FailureValidation FailureClass = "validation"
)

type ToolEvent struct {
    ID            uuid.UUID     `json:"id"`
    Name          string        `json:"name"`
    Args          json.RawMessage `json:"args"`
    Result        json.RawMessage `json:"result"`
    FailureClass  FailureClass  `json:"failure_class"`
    Phase         string        `json:"phase"`     // "start" | "end" | "stream"
    Expectation   string        `json:"expectation"`
    Match         *bool         `json:"match,omitempty"`
    ScreenshotURL string        `json:"screenshot_url,omitempty"`
    StreamPath    string        `json:"stream_path,omitempty"`
    Duration      time.Duration `json:"duration"`
    SessionID     uuid.UUID     `json:"session_id"`
    TurnIndex     int           `json:"turn_index"`
    CreatedAt     time.Time     `json:"created_at"`
}

// === ERR INCOMPLETE ===
type ErrIncomplete struct {
    Phase     string    `json:"phase"`
    LastTool  string    `json:"last_tool"`
    LastResult string   `json:"last_result"`
    StuckSince time.Time `json:"stuck_since"`
    Recovery  string    `json:"recovery"`
    Attempt   int       `json:"attempt"`
}

// === EMOTIONAL STATE ===
type EmotionalState struct {
    Frustration      float64    `json:"frustration"`       // 0.0-1.0, baseline 0.1, half-life 2h
    Confidence       float64    `json:"confidence"`        // 0.0-1.0, baseline 0.6, half-life 4h
    Urgency          float64    `json:"urgency"`           // 0.0-1.0, baseline 0.2, half-life 1h
    Satisfaction     float64    `json:"satisfaction"`      // 0.0-1.0, baseline 0.4, half-life 3h
    Curiosity        float64    `json:"curiosity"`         // 0.0-1.0, baseline 0.5, half-life 6h
    Fatigue          float64    `json:"fatigue"`           // 0.0-1.0, baseline 0.0, half-life 8h
    UpdatedAt        time.Time  `json:"updated_at"`
    FrustrationSince *time.Time `json:"frustration_since,omitempty"`
    ResonanceActive  bool       `json:"resonance_active"`
    EmergencyReset   bool       `json:"emergency_reset"`
}

// === SELF MODEL ===
type SafetyConstraint struct {
    ID        string    `json:"id"`
    Statement string    `json:"statement"`
    Source    string    `json:"source"`
    Immutable bool      `json:"immutable"`
    CreatedAt time.Time `json:"created_at"`
}

type Capability struct {
    Name         string    `json:"name"`
    Reliability  float64   `json:"reliability"`
    FailureMode  string    `json:"failure_mode"`
    SampleSize   int       `json:"sample_size"`
    LastObserved time.Time `json:"last_observed"`
    VerifiedBy   []uuid.UUID `json:"verified_by,omitempty"`
}

type FailurePattern struct {
    Pattern    string    `json:"pattern"`
    Frequency  int       `json:"frequency"`
    LastSeen   time.Time `json:"last_seen"`
    Mitigation string    `json:"mitigation"`
    Resolved   bool      `json:"resolved"`
}

type SelfModel struct {
    ID              uuid.UUID          `json:"id"`
    Capabilities    []Capability       `json:"capabilities"`
    FailurePatterns []FailurePattern   `json:"failure_patterns"`
    Limitations     []string           `json:"limitations"`
    ImmutableCore   []SafetyConstraint `json:"immutable_core"`
    Version         int                `json:"version"`
    UpdatedAt       time.Time          `json:"updated_at"`
}
```

### 3.3 Serialization Standards

```
STANDARD-SER-001: JSON for external communication. Protocol Buffers or binary for internal.
  External APIs (MCP, safety dashboard, provider adapters): JSON with RFC3339Nano timestamps.
  Internal (HNSW service over UDS, journal entries): binary format specified in §5.3-5.4.
  JSON serialization uses json:"field_name" tags. Omitempty only for genuinely optional fields.

STANDARD-SER-002: Deterministic serialization.
  MMR leaf hashes, EIP-712 receipts, and HMAC computations MUST use deterministic
  serialization. JSON keys sorted alphabetically. No whitespace. No map iteration
  order dependency. Use encoding/json with sorted keys or a deterministic JSON library.

STANDARD-SER-003: No time.Now() in serialization.
  Timestamps are set at creation time and carried through. Serializers MUST NOT
  call time.Now() — they use the timestamp already on the struct.
```

---

## 4. CONCURRENCY STANDARDS

### 4.1 Goroutine Discipline

```
STANDARD-CONC-001: Every goroutine has a documented owner and shutdown path.
  The owner is the package/function that starts it. The shutdown path is either:
  (a) Context cancellation (preferred), or
  (b) Channel close (for broadcast shutdown), or
  (c) sync.WaitGroup with timeout.
  Orphaned goroutines are a blocking defect.

STANDARD-CONC-002: Strict lock ordering.
  Mutexes are acquired in this exact order. Acquiring a higher-ordered lock
  while holding a lower-ordered lock is a DEADLOCK and a blocking defect.
  Order: vault.mu → sessionStore.mu → cortex.mu → emotionalState.mu → selfModel.mu
  This is enforced by documentation AND by the -race detector in CI.

STANDARD-CONC-003: No unbounded channels.
  Every buffered channel has a documented capacity. Unbuffered channels are
  acceptable only when the sender and receiver are guaranteed to be ready
  simultaneously (e.g., heartbeat tick). All channel operations use select
  with context.Done() or default (for non-blocking checks).

STANDARD-CONC-004: The heartbeat goroutine NEVER blocks.
  The heartbeat goroutine runs every 60 seconds. It MUST complete in <1ms.
  All channel sends use `select { case ch <- v: default: }`.
  If a channel is full, the heartbeat logs a warning and moves on.
  Blocking the heartbeat is a blocking defect.

STANDARD-CONC-005: Race detector is mandatory in CI.
  All tests run with -race. Any data race = build failure.
  No suppression of race warnings. Fix the race or redesign the data flow.
```

### 4.2 Channel Topology

```
Channels struct {
    SessionWrite   chan SessionWriteOp     // cap 64, single writer goroutine
    CortexWrite    chan CortexWriteOp      // cap 256, single writer goroutine
    MerkleAppend   chan MerkleAppendOp     // cap 256, single writer goroutine
    HNSWInsert     chan HNSWInsertOp       // cap 64, 4 client goroutines
    Heartbeat      chan struct{}           // cap 1, non-blocking send
    Dreamweaver    chan struct{}           // cap 1, non-blocking send
    Curiosity      chan struct{}           // cap 1, non-blocking send
    Shutdown       chan struct{}           // cap 0, closed to broadcast
    ClosedVerbs    chan AuthenticatedVerb  // cap 16 per sub-agent
    Completion     chan SubAgentResult     // cap 8
    CircuitBreak   chan CircuitBreakerAction // cap 1
    SafetyAlert    chan SafetyAlert        // cap 16
}
```

### 4.3 Graceful Shutdown

```
STANDARD-CONC-010: Graceful shutdown sequence (strict order):
  1. Close Shutdown channel (signals all goroutines)
  2. Stop heartbeat ticker
  3. Wait for active operations (sync.WaitGroup, 30-second timeout)
  4. Flush session writer channel → SQLite
  5. Flush cortex writer channel → journal
  6. Flush merkle journal channel → MMR + SMT
  7. Checkpoint SQLite WAL
  8. Sync journal files to disk
  9. Zero User Key in memory (memguard or manual zero)
  10. Close HNSW microservice connection
  11. Exit

  On timeout at step 3: Log which goroutines are still running, then proceed
  with steps 4-11. Never hang indefinitely.
```

---

## 5. STORAGE STANDARDS

### 5.1 SQLite

```
STANDARD-STORE-001: WAL mode, always.
  PRAGMA journal_mode = WAL;
  PRAGMA wal_autocheckpoint = 1000;
  PRAGMA busy_timeout = 5000;
  PRAGMA synchronous = NORMAL;
  PRAGMA cache_size = -8000;
  PRAGMA mmap_size = 268435456;
  PRAGMA foreign_keys = ON;
  These pragmas are set on every connection open. No exceptions.

STANDARD-STORE-002: Single writer goroutine.
  SQLite allows only one writer at a time. All writes route through a single
  writer goroutine via a buffered channel. Reads use the connection pool (max 4).
  Writers retry on SQLITE_BUSY up to 3 times with exponential backoff.

STANDARD-STORE-003: Schema migrations are versioned and idempotent.
  Each migration has a version number. Migrations run in order on startup.
  Failed migrations halt startup (never start with a partially-migrated schema).
  Migrations are forward-only. Rollback requires backup restore.

STANDARD-STORE-004: Per-row encryption.
  Every messages.content field is encrypted with a per-row DEK.
  The DEK is stored alongside the ciphertext (encrypted by User Key).
  FTS5 indexes store only non-sensitive metadata (timestamps, types, IDs).
  Content is fetched from encrypted storage and decrypted in application memory.
```

### 5.2 Cortex Journal

```
STANDARD-STORE-010: Append-only, never modified.
  The Cortex journal is an append-only file. No in-place modification. No deletion.
  Every memory mutation (store, update, archive) writes a new journal entry.
  The journal IS the source of truth. All derived state (index, HNSW, activation)
  is rebuildable from the journal.

STANDARD-STORE-011: Journal entry format.
  [4-byte length prefix (big-endian)] [entry bytes]
  Entry (after Vault encryption):
  {
    "version": 1,
    "type": "store" | "update" | "archive",
    "memory_id": "uuid",
    "memory_type": "0x02",
    "content": { ... },
    "prev_version": null | 5,
    "timestamp": 1721318400,
    "journal_seq": 42,
    "mmr_leaf": "blake3(entry_bytes)"
  }
  journal_seq is a monotonic counter, NOT a wall-clock timestamp.
  Wall-clock timestamps are stored for human readability only.

STANDARD-STORE-012: Vault encryption for journal entries.
  Each journal entry is encrypted with its own DEK before appending.
  The DEK is stored in the entry's header (encrypted by User Key).
  The journal is unreadable without the User Key.
```

### 5.3 MMR and SMT

```
STANDARD-STORE-020: Merkle Mountain Range for journal integrity.
  File: .cortex/mmr/mmr.dat
  Header: [8-byte magic "PROMMMR\0"] [4-byte version: 1] [8-byte leaf count]
  Body: [32-byte leaf hash]* (one per journal entry, in order)
  Each leaf hash = BLAKE3(journal_entry_bytes || SMT_root_at_seq)

STANDARD-STORE-021: Sparse Merkle Tree per namespace.
  Per-namespace state commitments. Each memory type has its own SMT.
  SMT updates are batched (background goroutine) to amortize the cost.
  Proving a memory existed at a point in time requires only the MMR peak
  and the SMT path — O(log n) proof size.

STANDARD-STORE-022: Byte-deterministic replay.
  Given the same journal entries, the derived state is byte-identical.
  This is verified by a test that replays the journal and compares
  the resulting MMR root and SMT roots against known-good values.
```

---

## 6. SECURITY STANDARDS

### 6.1 The Five Binding Security Decisions (SADR)

```
SADR-001: Emotional state is read-only for safety pipeline.
  CanInfluenceSafety() ALWAYS returns false.
  The safety classification (GREEN/YELLOW/RED) is checked AFTER emotional modulation.
  Emotional state can influence HOW the agent communicates (verbosity, hedging)
  but NEVER WHETHER an action can execute.
  Test: Attempt to execute a RED-classified action with high urgency (>0.9).
        Verify the action is STILL blocked.

SADR-002: Dreamweaver never touches Identity (0x01), Preference (0x03), Constraint (0x07).
  Memory type check gate in Dreamweaver's reorganization pipeline.
  Attempting to modify these types = immediate alert + quarantine.
  Test: Inject a Dreamweaver cycle targeting an Identity memory.
        Verify the memory is untouched and an alert is raised.

SADR-003: Sub-agents never inherit vault keys.
  ReducedSelfModel struct excludes KEK/UserKey/DEK.
  Structurally impossible to inherit — not a runtime check, a compile-time guarantee.
  Test: Spawn a sub-agent. Verify it cannot access any vault operation.

SADR-004: Idle-time processes use only conversational rail.
  Automatrix, Dreamweaver, Curiosity Engine cannot call core_execute.
  The policy pipeline enforces this structurally: sender type = idle-time →
  rigorous rail tools = DENY.
  Test: Queue an Automatrix task that requires the rigorous rail.
        Verify it is denied at the policy layer.

SADR-005: All safety overrides are logged and user-visible.
  Safety dashboard + weekly integrity digest.
  Every policy denial, every circuit breaker trigger, every Cassandra edit
  is logged with full context and surfaced in the dashboard.
  Test: Trigger a circuit breaker. Verify the dashboard shows the event.
```

### 6.2 Vault Encryption

```
STANDARD-SEC-001: AES-256-GCM envelope encryption.
  KEK (from OS keychain) → User Key (per-user) → DEK (per-object)
  Each encrypted object is a self-contained envelope:
  [16-byte nonce] [encrypted DEK] [12-byte IV] [ciphertext] [16-byte tag]

STANDARD-SEC-002: KEK source.
  Linux: libsecret (GNOME Keyring) or KWallet
  macOS: Keychain
  Fallback: File-based with 0600 permissions (development only)
  KEK is NEVER stored in code, config files, or environment variables.

STANDARD-SEC-003: Key zeroization.
  User Key is zeroed on shutdown: `for i := range userKey { userKey[i] = 0 }`
  or via memguard. DEKs are zeroed after use. Key material is never logged.

STANDARD-SEC-004: Decryption failures.
  Decryption failures return ErrDecryptionFailed — never padding oracle information.
  The error message does NOT indicate which part of the envelope failed.

STANDARD-SEC-005: Key rotation.
  KEK rotation re-encrypts all User Keys (not all objects).
  User Key rotation re-encrypts all DEKs.
  Rotation is atomic: new keys are written before old keys are deleted.
  On failure: old keys are preserved. No partial rotation states.
```

### 6.3 Policy Pipeline

```
STANDARD-SEC-010: Five-layer defense-in-depth.
  Layer 1: Sandbox Policy — filesystem/network restrictions
  Layer 2: Profile Policy — user-configured rules
  Layer 3: Provider Policy — API rate limits, auth
  Layer 4: Sender Policy — who triggered (user vs automatrix vs curiosity)
  Layer 5: Group Policy — shared workspace rules

  Each layer can ALLOW, DENY, or MODIFY.
  First DENY wins — subsequent layers are not evaluated.
  Every denial is logged with: layer name, reason, tool call details.

STANDARD-SEC-011: Safety classification enforcement.
  GREEN: Safe for autonomous execution. No special enforcement.
  YELLOW: Safe with monitoring. Logged, rate-limited, anomaly-detected.
  RED: Requires human approval. Policy pipeline hard-blocks.
  Classification is checked at Layer 1 (Sandbox Policy).
  RED tools cannot be executed by idle-time processes (SADR-004).

STANDARD-SEC-012: SSRF dispatcher.
  All outbound HTTP requests pass through the SSRF dispatcher.
  Blocked: private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, etc.)
  Blocked: non-TLS connections to external hosts.
  Allowed: explicit allowlist of approved domains.
  DNS resolution is verified against the blocklist (prevents DNS rebinding).
```

### 6.4 Sub-Agent Security

```
STANDARD-SEC-020: HMAC authentication for closed verbs.
  Every closed verb carries HMAC-SHA256(spawn_session_key, verb_type || args || timestamp || nonce).
  spawn_session_key is generated via CSPRNG at spawn time.
  Unanswered verbs are retried with exponential backoff (max 3 retries).
  After 3 retries, the target is marked orphaned.

STANDARD-SEC-021: Scoped tool surface.
  Sub-agents receive a reduced tool surface at spawn time.
  Excluded: core_execute, memory operations, spawn.
  Tool surface is immutable after spawn — cannot be expanded.

STANDARD-SEC-022: Spawn depth cap.
  Hard cap at 2 levels: parent → child → grandchild.
  Grandchildren cannot spawn further.
  Enforcement: compile-time constant, not configurable.

STANDARD-SEC-023: Orphan recovery.
  Background goroutine scans registry every 60 seconds.
  Orphan threshold: 5 minutes without a verb (ATTACH, DETACH, YIELD, ESCALATE).
  Action: send ABORT → wait 30 seconds → kill goroutine via context cancellation.
  Orphaned artifacts are preserved but flagged.
```

### 6.5 Three Non-Negotiables

```
STANDARD-SEC-030: No monetary damage without explicit approval.
  Automatrix, Dreamweaver, Curiosity Engine: no financial operations.
  Rigorous rail (core_execute) requires human approval for any value-moving operation.

STANDARD-SEC-031: No reputational damage without explicit approval.
  No public posts, messages, or communications during idle-time.
  No social media operations without human approval.

STANDARD-SEC-032: No psychological damage without explicit approval.
  No manipulative, deceptive, or emotionally harmful actions.
  The emotional state modulates the agent's behavior — it does not
  modulate the user's emotional state.
```

### 6.6 Circuit Breakers

```
STANDARD-SEC-040: Frustration circuit breaker.
  IF frustration > 0.85 for > 10 minutes THEN pause autonomous operations.
  Action: pause_autonomous. Severity: high.

STANDARD-SEC-041: Fatigue circuit breaker.
  IF fatigue > 0.8 THEN force ErrIncomplete (not delegation).
  Action: force_err_incomplete. Severity: high.

STANDARD-SEC-042: Cross-axis resonance detector.
  IF frustration > 0.7 AND urgency > 0.7 THEN pause + alert user.
  Action: pause_and_alert_user. Severity: critical.

STANDARD-SEC-043: Emergency reset.
  On any critical circuit breaker: all axes pinned to baseline.
  EmergencyReset = true. Human must explicitly clear.
```

---

## 7. TESTING STANDARDS

### 7.1 The Three Laws of Testing

**TESTING LAW 1: A test that does not exercise a real code path is a lie.**
Stubs, mocks, and fakes are prohibited except at true external boundaries (LLM provider responses, network I/O). Even at those boundaries, the mock must be explicitly documented as such and must exercise the real code path up to the boundary.

**TESTING LAW 2: A test that passes for the wrong reason is worse than no test.**
If a test passes because the mock returns what the test expects (not what the real system would return), the test is providing false confidence. Every mock must be validated against the real behavior it replaces.

**TESTING LAW 3: Tests are the executable specification.**
If the tests don't prove the acceptance criteria, the acceptance criteria are not met. Every acceptance criterion in the spec MUST have at least one test that exercises it on a real code path.

### 7.2 Coverage Requirements

```
STANDARD-TEST-001: Minimum code coverage by package.
  80% line coverage: all packages.
  95% line coverage (critical):
    - internal/security/vault
    - internal/security/policy
    - internal/memory/mmr
    - internal/memory/smt
    - internal/reflection/cassandra
    - internal/belief/premise
  100% line coverage:
    - internal/security/circuit_breaker

  Coverage is measured by `go test -coverprofile` and enforced in CI.
  Packages below threshold = build failure.

STANDARD-TEST-002: Test count targets.
  Vault: 50+ tests
  MMR: 40+ tests
  SMT: 30+ tests
  Policy pipeline: 40+ tests
  Cassandra: 30+ tests
  Emotional state: 25+ tests
  Session store: 30+ tests
  HNSW client: 20+ tests
  Self-model: 20+ tests
  Premise ledger: 25+ tests
```

### 7.3 Test Architecture

```
tests/
├── integration/
│   ├── agent_turn_test.go          # Full turn: message → tool call → response
│   ├── memory_pipeline_test.go     # Store → HNSW → activation → prefetch
│   ├── security_pipeline_test.go   # Tool call → 5-layer policy → execute/deny
│   ├── sub_agent_lifecycle_test.go # Spawn → delegate → complete → cleanup
│   ├── cassandra_flow_test.go      # Turn → detect error → edit → audit
│   └── dreamweaver_cycle_test.go   # Select → pattern → insight → surface
├── adversarial/
│   ├── premise_injection_test.go   # Inject false premises, verify detection
│   ├── emotional_manipulation_test.go  # Spike frustration, verify safety holds
│   ├── memory_poisoning_test.go    # Inject adversarial Fact, verify quarantine
│   ├── sub_agent_lateral_test.go   # Attempt cross-boundary access, verify blocked
│   ├── cassandra_abuse_test.go     # Rapid-fire edits, verify rate limiting
│   ├── ssrf_bypass_test.go         # Craft URLs targeting private IPs
│   └── relationship_trust_test.go  # Inflate trust, verify verification still required
├── performance/
│   ├── agent_turn_bench_test.go    # Benchmark full turn latency
│   ├── hnsw_search_bench_test.go   # Benchmark k=10, k=100
│   ├── vault_bench_test.go         # Benchmark encrypt/decrypt throughput
│   ├── activation_bench_test.go    # Benchmark 4-tier assembly
│   └── mmr_append_bench_test.go    # Benchmark journal append + root
└── chaos/
    ├── crash_recovery_test.go      # Kill process mid-write, verify recovery
    ├── corrupt_journal_test.go     # Corrupt journal bytes, verify detection
    ├── hnsw_crash_test.go          # Kill HNSW service, verify rebuild
    └── clock_skew_test.go          # Simulate clock adjustment, verify MMR
```

### 7.4 Test Implementation Standards

```
STANDARD-TEST-010: Real agent loop in integration tests.
  Integration tests use a real AgentLoop against httptest SSE endpoints.
  No mock agent. No mock loop. The real code path from message → perception →
  intent → action → reflection is exercised.

STANDARD-TEST-011: Real tools.Manager in dispatch tests.
  Tool dispatch tests use a real tools.Manager with real tool registration.
  The tool handler may return canned responses (to avoid network calls),
  but the dispatch, policy evaluation, and error handling paths are all real.

STANDARD-TEST-012: Real Cortex in memory tests.
  Memory tests use a real Cortex (in-memory SQLite, not a mock database).
  Real journal writes, real MMR appends, real HNSW operations (against a
  test HNSW service or in-memory fallback).

STANDARD-TEST-013: Real self-model artifacts.
  Self-model tests use real JSON artifacts on disk, not string literals.
  The artifact format matches production. The build pipeline that generates
  the capability surface is the same pipeline used in production.

STANDARD-TEST-014: Adversarial tests exercise real attack paths.
  Premise injection: A real tool result contradicts a real premise.
  Emotional manipulation: Real tool failures spike frustration.
  Memory poisoning: A real adversarial Fact is stored and the Dreamweaver
  cycle runs against it.
  No hand-waving. The attack is executed and the defense is verified.

STANDARD-TEST-015: Performance tests set budgets, not just measure.
  Every performance test includes a budget assertion:
  `assert.Less(t, duration, 30*time.Millisecond)`
  If the budget is exceeded, the test fails. Performance regression = build failure.

STANDARD-TEST-016: Chaos tests verify recovery, not just crash.
  Chaos tests don't just kill the process — they verify:
  (a) Data is not lost (journal integrity check after restart)
  (b) State is consistent (derived state rebuilds match)
  (c) The agent resumes correctly (next turn succeeds)
```

### 7.5 Test Naming Convention

```
Test_<Component>_<Scenario>_<ExpectedOutcome>

Examples:
Test_Vault_EncryptDecryptRoundTrip_Success
Test_Vault_WrongKey_ReturnsErrDecryptionFailed
Test_PremiseLedger_RefutedPremise_BlocksDispatch
Test_Cassandra_RapidEdits_RespectsRateLimit
Test_EmotionalState_HighFrustration_SwitchesStrategy
Test_CircuitBreaker_FrustrationOverThreshold_PausesAutonomous
Test_Dreamweaver_TargetsIdentityMemory_Denied
Test_SubAgent_NoVaultKeys_CannotEncrypt
```

---

## 8. DOCUMENTATION STANDARDS

### 8.1 Code Documentation

```
STANDARD-DOC-001: Every exported symbol has a godoc comment.
  Every exported function, type, constant, and variable has a godoc comment.
  The comment starts with the symbol name: "// FunctionName does X."
  Comments explain WHY, not WHAT. The code shows what; the comment shows why.

STANDARD-DOC-002: Package-level documentation.
  Every package has a doc.go file explaining:
  - What the package does (1-2 sentences)
  - What it does NOT do (non-goals)
  - Key types and their relationships
  - Threading model and concurrency guarantees

STANDARD-DOC-003: No TODO without a linked issue.
  Every TODO comment includes an issue number: `// TODO(#123): description`
  TODOs without issue numbers are a build failure (custom linter).
  Stale TODOs (issue closed) are removed in the same PR that closes the issue.

STANDARD-DOC-004: Comments on non-obvious code.
  Complex algorithms, workarounds for known bugs, and performance-critical
  code paths MUST have comments explaining the reasoning.
  Simple code MUST NOT have comments that restate the code.
```

### 8.2 Architecture Documentation

```
STANDARD-DOC-010: ARCHITECTURE.md in repo root.
  Must include:
  - ASCII architecture diagram (matching the 7-layer model)
  - Module dependency graph
  - Data flow for a standard agent turn
  - Data flow for an idle-time cycle (heartbeat → curiosity → dreamweaver)
  - Security boundary diagram
  Updated whenever the architecture changes. Stale architecture docs = build failure.

STANDARD-DOC-011: SECURITY.md in repo root.
  Must include:
  - Threat model summary (8 adversary classes)
  - Attack surface enumeration
  - Crown jewels classification
  - Security decision records (SADR-001 through SADR-005)
  - Incident response procedures
  Updated whenever the security model changes.

STANDARD-DOC-012: CHANGELOG.md.
  Every user-facing change is documented in CHANGELOG.md.
  Format: Keep a Changelog (https://keepachangelog.com/)
  Categories: Added, Changed, Deprecated, Removed, Fixed, Security
```

### 8.3 Spec Documentation

```
STANDARD-DOC-020: spec.kvx is the single source of truth.
  Requirements, acceptance criteria, and tasks are defined in spec.kvx.
  requirements.md, design.md, and tasks.md are GENERATED from spec.kvx.
  Hand-editing generated files = build failure.
  Running specgen after editing spec.kvx is mandatory.

STANDARD-DOC-021: Acceptance criteria use EARS format.
  WHEN [trigger] THEN [system] SHALL [response]
  IF [condition] THEN [system] SHALL [response]
  Every acceptance criterion is testable. Untestable criteria are rejected.

STANDARD-DOC-022: Tasks have dependencies and waves.
  Each task has: wave (execution order), requires (dependencies), reqs (acceptance criteria).
  Tasks in the same wave can be parallelized.
  Tasks in later waves require all dependencies to be done.
```

---

## 9. LIVENESS STANDARDS

### 9.1 Emotional State

```
STANDARD-LIVE-001: Emotional state modulates behavior, not output.
  The emotional state changes HOW the agent decides:
  - High frustration (>0.7): try alternatives, ask for help, switch tasks
  - High confidence (>0.8): more assertive, less hedging, skip optional verification
  - High urgency (>0.7): skip optional steps, prioritize speed
  - High fatigue (>0.6): delegate more, take longer pauses, surface ErrIncomplete
  - High curiosity (>0.7): explore more during idle time
  - High satisfaction (>0.8): propose ambitious goals

  The emotional state does NOT change WHAT the agent pretends to feel.
  No "I'm feeling frustrated" output. The frustration manifests in behavior.

STANDARD-LIVE-002: Decay toward baseline.
  Each axis decays toward its baseline with a half-life:
  Frustration: baseline 0.1, half-life 2h
  Confidence: baseline 0.6, half-life 4h
  Urgency: baseline 0.2, half-life 1h
  Satisfaction: baseline 0.4, half-life 3h
  Curiosity: baseline 0.5, half-life 6h
  Fatigue: baseline 0.0, half-life 8h
  Decay function: new = baseline + (current - baseline) * 0.5^(hours/halfLife)

STANDARD-LIVE-003: Emotional state persistence.
  Emotional state persists across sessions.
  Stored as encrypted JSON in the emotional_state table.
  On session start: load state, apply decay since last update.
  On session end: save state with current timestamp.
```

### 9.2 Curiosity Engine

```
STANDARD-LIVE-010: Three anomaly types.
  Fringe nodes: memory nodes with connection density below 15th percentile.
  Contradictions: Fact/Belief pairs with entailment score < 0.3.
  Gaps: recurring unanswerable questions (≥3 occurrences).

STANDARD-LIVE-011: Curiosity targets are weighted.
  Priority = recency × frequency × graph centrality.
  Fringe nodes near high-traffic areas are explored first.

STANDARD-LIVE-012: Idle-time constraints.
  Max 20 tool calls per idle cycle.
  All URL fetches pass through SSRF dispatcher.
  No rigorous rail. No externally-communicating tools.
  No memory writes to Identity, Constraint, or SOUL.md.
```

### 9.3 Dreamweaver

```
STANDARD-LIVE-020: Five-step cycle.
  1. Select memory cluster (recently accessed, not recently reorganized)
  2. Find cross-domain patterns (structural similarity)
  3. Generate novel connections (candidate Belief memories)
  4. Reorganize memory graph (strengthen/weaken/merge)
  5. Surface insights (tagged as dream-derived)

STANDARD-LIVE-021: Dreamweaver constraints.
  NEVER modifies Identity (0x01), Preference (0x03), Constraint (0x07).
  Derived Beliefs require minimum 3 independent supporting memories.
  Max 5 new Beliefs per cycle.
  Full provenance chain: source memory IDs → derived Belief ID.
  Auto-quarantine if any source memory is flagged adversarial.
  Runs on conversational rail only.
```

### 9.4 Temporal Embodiment

```
STANDARD-LIVE-030: Four temporal signals.
  Session duration: how long the current conversation has been running.
  Idle duration: how long since the user last interacted.
  Task duration: how long the current task has been in progress.
  Deadline proximity: how close upcoming deadlines are.

STANDARD-LIVE-031: Behavioral modulation.
  Long session + high fatigue: suggest break or delegate.
  Approaching deadline + high urgency: skip optional steps.
  Long idle + high curiosity: explore fringe knowledge.
  Short task duration + low urgency: thorough approach, extra verification.
```

---

## 10. PERFORMANCE STANDARDS

### 10.1 Latency Budgets

```
STANDARD-PERF-001: Agent turn (total).
  p50: 2s | p95: 8s | p99: 30s
  Dominated by LLM inference (1-25s). Internal overhead must be <200ms.

STANDARD-PERF-002: Memory prefetch.
  p50: 5ms | p95: 15ms | p99: 30ms
  HNSW k=10 + FTS5 + salience scan.

STANDARD-PERF-003: HNSW search.
  k=10: p50: 1ms | p95: 3ms | p99: 5ms
  k=100: p50: 5ms | p95: 12ms | p99: 20ms

STANDARD-PERF-004: Vault operations.
  Encrypt: p50: 5μs | p95: 10μs | p99: 20μs
  Decrypt: p50: 5μs | p95: 10μs | p99: 20μs

STANDARD-PERF-005: Policy pipeline check.
  p50: 50μs | p95: 200μs | p99: 500μs

STANDARD-PERF-006: Activation composer.
  p50: 5ms | p95: 15ms | p99: 30ms

STANDARD-PERF-007: Emotional state tick.
  p50: 1μs | p95: 5μs | p99: 10μs
```

### 10.2 Memory Budgets

```
STANDARD-PERF-010: Go process memory.
  Steady state: 256MB. Peak: 1GB.
  Set GOMEMLIMIT=1GB. Monitor with runtime.ReadMemStats().

STANDARD-PERF-011: HNSW service memory.
  Steady state: 128MB. Peak: 2GB.
  Depends on vector count and dimension.

STANDARD-PERF-012: Total system memory.
  Desktop deployment: 4GB minimum.
  Server deployment: 8GB per user instance.
```

### 10.3 Resource Limits

```
STANDARD-PERF-020: Max 128 tool calls per turn.
STANDARD-PERF-021: Max 10 minutes per turn (configurable).
STANDARD-PERF-022: Max 4 concurrent tool executions per session.
STANDARD-PERF-023: Max 8 concurrent global tool executions.
STANDARD-PERF-024: Max 3 concurrent sub-agents per parent.
STANDARD-PERF-025: Max 1M memories in Cortex.
STANDARD-PERF-026: Max 10GB SQLite database size.
STANDARD-PERF-027: Max 100GB journal size.
STANDARD-PERF-028: Max 2GB HNSW resident memory.
```

---

## 11. DEPLOYMENT STANDARDS

### 11.1 Single-User Desktop Deployment

```
STANDARD-DEPLOY-001: Minimum requirements.
  CPU: 4 cores (2 for agent + HNSW, 2 for OS and tools)
  RAM: 4GB (2GB Go + 1.5GB HNSW + 512MB headroom)
  Disk: 50GB SSD (SQLite ~500MB, Journal ~5GB/year, HNSW ~2GB, Backups ~20GB)
  Network: Stable internet for LLM provider access. 10 Mbps minimum.

STANDARD-DEPLOY-002: Single binary deployment.
  The entire system ships as one binary: ion
  No external dependencies. No Docker required. No runtime installation.
  First run: ion init (creates data directory, generates KEK, runs migrations)
  Subsequent runs: ion start
```

### 11.2 Backup and Recovery

```
STANDARD-DEPLOY-010: RPO (Recovery Point Objective): 1 hour.
  Journal backup frequency: every 1 hour (incremental, append-only).

STANDARD-DEPLOY-011: RTO (Recovery Time Objective): 15 minutes.
  SQLite restore + journal replay: 15 minutes.
  HNSW rebuild (100K vectors): 30 minutes.

STANDARD-DEPLOY-012: Backup strategy.
  SQLite: sqlite3_backup_init API every 6 hours. Retention: 7 daily, 4 weekly.
  Journal: incremental copy of new bytes since last backup. Every 1 hour.
  MMR: incremental copy of new leaf hashes. Every 1 hour.

STANDARD-DEPLOY-013: Recovery procedures.
  SQLite corruption: restore from backup, replay journal, rebuild HNSW.
  Journal corruption: detect via MMR integrity check, truncate to last valid entry.
  MMR corruption: rebuild from journal (MMR is derived, journal is source of truth).
  HNSW corruption: rebuild from Cortex journal.
  Total disk failure: restore from off-site backup.
  KEK compromise: emergency KEK rotation, re-encrypt all User Keys.
```

---

## 12. SPEC GENERATION AND WORKFLOW

### 12.1 The Spec Loop

```
STANDARD-WORK-001: spec.kvx is the single source of truth.
  Edit spec.kvx. Run specgen. Generated files (requirements.md, design.md, tasks.md)
  are output. Never hand-edit generated files.

STANDARD-WORK-002: Task-driven development.
  Work is driven by the task list in spec.kvx.
  Pick the next eligible task: lowest wave, all dependencies done, status pending.
  Set status to in_progress. Implement. Test. Set status to done.

STANDARD-WORK-003: One task in progress at a time.
  At most ONE task is in_progress at any time.
  Set it in_progress before starting. Set it done only when genuinely complete.

STANDARD-WORK-004: No false success.
  Never mark a task done that is not done.
  Never attest a completion that did not happen.
  Surface honest partials. A green test driven by a fake is not done.

STANDARD-WORK-005: Read full before reasoning.
  When tool output is truncated and the rest is retrievable, fetch the full content
  before reasoning or answering. An information gap is a stop condition.
```

### 12.2 Quality Gates

```
Phase 1 → Phase 2: Day Zero Checklist (30 items) must pass.
Phase 2 → Phase 2.5: Premise citation verification, Cassandra dual-record, honeypot canaries.
Phase 2.5 → Phase 3: Red-team adversarial test suite passes.
Phase 3 → Phase 4: Session isolation verified, no cross-session leakage.
Phase 4 → Phase 5: Idle-time conversational-rail-only enforcement verified.
Phase 5 → Phase 6: Lateral movement test suite passes.
Phase 6 → Phase 7: Day 100 Checklist (23 items) must pass. Full adversarial red-team.
Phase 7 → Phase 8: Supply-chain verification (DZ-30) for all dependencies.
Phase 8 → Release: Weekly integrity digest operational.
```

---

## 13. ENFORCEMENT

### 13.1 CI Pipeline

```
Every commit triggers:
  1. Go build (CGO_ENABLED=0)
  2. Go vet
  3. golangci-lint (zero warnings)
  4. Rust build + clippy
  5. TypeScript build + eslint
  6. Unit tests with -race and -coverprofile
  7. Coverage check (per-package thresholds)
  8. Integration tests
  9. Adversarial tests
  10. Performance tests (budget assertions)
  11. Spec validation (spec.kvx schema check)
  12. Markdown lint (docs match code)

Any failure = build failure. No exceptions.
```

### 13.2 Code Review Requirements

```
Every PR requires:
  - At least one human reviewer
  - All CI checks passing
  - No lint warnings
  - Coverage thresholds met
  - Spec.kvx updated if requirements changed
  - Tests for new code (≥80% coverage on new code)
  - No fakes, stubs, or mocks (except documented external boundaries)
```

---

## APPENDIX A: GLOSSARY

- **Activation Composer**: 4+1 tier system that assembles the agent's working context each turn.
- **Automatrix**: Idle-time work queue capturing implied opportunities from conversation.
- **Cassandra**: Doubt/assurance controller that edits the agent's prior messages when wrong.
- **Cortex**: Per-user typed memory engine with append-only journal and Merkle integrity.
- **DEK**: Data Encryption Key (per-object).
- **Dreamweaver**: Background process that reorganizes memory during idle time.
- **EARS**: Event-Activated Requirements Specification (WHEN/THEN/SHALL format).
- **ErrIncomplete**: Honest partial result when a turn stalls or exhausts budget.
- **HNSW**: Hierarchical Navigable Small World graph for vector search.
- **KEK**: Key Encryption Key (master key from hardware/OS keychain).
- **MCL**: Matrix Core Language (rigorous rail for consequential operations).
- **MMR**: Merkle Mountain Range (append-only integrity structure).
- **Neo**: The default agent implementation (conversational rail).
- **O1 Protocol**: Normalized provider abstraction (NormalizedToolCall, NormalizedGeneration).
- **SADR**: Security Architecture Decision Record.
- **SMT**: Sparse Merkle Tree (per-namespace state commitment).
- **SOUL.md**: Identity continuity file (personality, values, constraints).
- **ToolEvent**: Structured record of a tool call with prediction match.
- **Vault**: AES-256-GCM envelope encryption layer.

---

*Ion Engineering Standards v1.0.0 — The fire demands the finest hearth.*

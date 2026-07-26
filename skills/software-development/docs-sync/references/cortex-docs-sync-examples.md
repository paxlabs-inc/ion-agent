# Cortex Docs Sync — Concrete Discrepancy Patterns Found

Session: 2026-07-14. Synced 13 `.mdx` reference docs against `matrix-core/cortex/` Go source.

## Discrepancies by category

### 1. Struct field drift (most common)

**`Query` struct** — `find-query.mdx` was missing:
- `RankMode RankMode` field (v3 #4 — salience vs distance ranking for Near queries)
- `AsOf *time.Time` field (v3 #2 — bi-temporal valid-time filter)
- `NearVector []float32`, `NearIndex *vector.Index`, `NearK int` (Phase 5 vector search internals)

**`Result` struct** — `find-query.mdx` had wrong field types:
- `Scores` was `[]float32` (parallel-indexed), actual is `map[memory.ID]float32`
- `HopDistances` was `[]int`, actual field is `Hops map[memory.ID]int`
- Missing `Distances map[memory.ID]float32` (HNSW distance map)
- Missing `CandidatesScanned int`, `Total int`

**`Selector` struct** — `scope.mdx` had wrong field:
- Listed `Frames []memory.FrameRef`, actual is `Frame *FrameFilter` (different type, different semantics)

**`Score` struct** — `salience.mdx` was missing:
- `Pinned bool` field (cbor key 5)

**`Version` struct** — `memory-taxonomy.mdx` was missing:
- `ValidFrom *time.Time` (cbor key 13)
- `ValidUntil *time.Time` (cbor key 14)
- `ExpiresAt *time.Time` (cbor key 7)

### 2. Missing journal kinds

**`store-and-journal.mdx`** — missing:
- `KindLearnWeights` (always follows `KindAttest` in same batch, Phase 12)
- `KindAttest` entry was missing `AccessedIDs[]` field note for replay

### 3. Missing enum/type definitions

**`memory-taxonomy.mdx`** — missing:
- `ID` type definition and `NewID()`/`ParseID()` constructors
- `VectorRef` struct
- `VectorMeta` struct
- `SourceKind` enum values
- `Provenance` struct
- Bi-temporal valid-time fields on Version

**`edges-and-graph.mdx`** — listed only 6 edge types, source has 14. All byte values needed verification.

### 4. Wrong function signatures

**`store-and-journal.mdx`** — `IterJournal` signature was:
```go
// WRONG (doc)
err = s.IterJournal(fromSeq, func(e *journal.Entry) error { ... })
// CORRECT (source)
err = s.IterJournal(func(e *journal.Entry) error { ... })
```

### 5. Missing features from later phases

**`overview.mdx`** — repo layout was missing `replay/rebuild.go`

**`store-and-journal.mdx`** — missing:
- Multi-entry atomic batch support (`WriteBatch` supports multiple `AppendJournal` calls)
- `BatchedReader` interface for in-batch sibling reads
- `meta/snapshot_seq` in important meta/ keys table

**`replay.mdx`** — missing:
- Phase 11.5 salience bump replay (`rebuildSalienceFromJournal`)
- `KindFind`/`KindAttest`/`KindLearnWeights` handling in rebuild
- `SalienceBumpsApplied` in Result struct
- `meta/salience_weights` in derived single-key markers

### 6. Wrong struct/type shapes in docs

**`snapshot-and-proofs.mdx`** — `OverallRoot` formula was simplified:
```go
// WRONG (doc) — missing domain prefix, schema version, ns count
OverallRoot = sha256(journal_root || memories_root || edges_root)
// CORRECT (source)
OverallRoot = sha256(OverallRootDomain || SchemaVersion(1B) || journalRoot || nsCount(2B) || for each ns sorted: lpString(ns) || stateRoot)
```

**`attest-and-compact.mdx`** — `AttestResult` was missing:
- `PrevWeights salience.Weights`
- `NewWeights salience.Weights`
- `WeightsUpdated bool`

### 7. Missing error types

**`scope.mdx`** — missing `ErrSignatureInvalid` in error reference

**`attest-and-compact.mdx`** — missing `ErrAttestRateLimited` (Phase 14 rate limiting)

### 8. Stale design decision claims

**`salience.mdx`** — was missing:
- `HalfLifeNanos` constant (90 days, but is 1/e decay not true half-life)
- `AccessSaturation` constant (1000.0, not per-actor max)
- `BumpForAccess` helper in bump helpers table
- Cold start `ReadWeights` behavior documented

## Detection strategy that worked

1. Read ALL source files first (parallel), noting every exported type/func/const
2. Read ALL existing docs (parallel), noting every claim
3. For each doc page, compare claimed structs against actual source struct field-by-field
4. For each enum/const table, verify every value against source
5. For each code example, verify field names and types are current
6. Rewrite whole files (don't patch individual fields — internal consistency matters)

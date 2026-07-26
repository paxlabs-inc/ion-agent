# Matrix-Core Subsystem Map

Detailed file-level map of key interfaces and entry points discovered during codebase exploration (2026-07-20).

## Merkle Tree — `codegraph/merkle/merkle.go`

```go
// Core types
type Tree struct {
    Root       string              // "b3:<hex>" root hash
    files      map[string]string   // path -> leaf hash
    dirs       map[string]string   // dir -> subtree hash
    childFiles map[string][]string
    childDirs  map[string][]string
}

type Changes struct {
    Added   []string
    Removed []string
    Changed []string
}

// Key functions
func LeafHash(content []byte) string           // BLAKE3 over normalized content
func FromLeaves(leaves map[string]string) *Tree // Build from precomputed hashes
func FromContentMap(contents map[string][]byte) *Tree
func Diff(old, new *Tree) Changes              // Tandem tree walk, skip unchanged subtrees
```

Domain prefixes: `matrix.codegraph.merkle.leaf.v1\0`, `matrix.codegraph.merkle.node.v1\0`.

## Self-Model — `codegraph/selfmodel/selfmodel.go`

```go
type Artifact struct {
    Summary string   `json:"summary"`
    Merkle  string   `json:"merkle"`
    Scope   []string `json:"scope"`
}

func Distill(ix *model.Index, merkle string, scope []string, tokenBudget int) Artifact
func Lookup(ix *model.Index, symbol string) (string, error)
```

`residentSymbols` defines which code symbols are included in self-description: `Chat`, `assembleWindowUserTail`, `runSwarm`, `SubagentSchemas`, `AssertNoValueTransferTools`, `coreExecuteSchema`.

## Digest — `codegraph/model/digest.go`

```go
func Digest(src []byte) string  // BLAKE3 over normalized source, returns "b3:<hex>"
func NormalizeSource(src []byte) []byte  // CRLF→LF, strip trailing whitespace
```

## Snapshot — `cortex/snapshot/snapshot.go`

```go
type Manifest struct {
    SchemaVersion uint8
    Actor         string
    SeqAtSnapshot uint64
    JournalSeq    uint64
    JournalRoot   [32]byte
    StateRoots    map[string][32]byte  // "memories", "edges"
    OverallRoot   [32]byte             // SHA-256 composition
    CreatedAt     int64                // unix nanos
    Trigger       string               // "compile"|"attest"|"periodic"|"explicit"
    SignedBy      string
    Signature     []byte
    Counters      Counters
}

func ComputeOverallRoot(journalRoot [32]byte, stateRoots map[string][32]byte) [32]byte
```

Anchored namespaces: `["edges", "memories"]` (alphabetical order for canonical encoding).

## Identity — `MCL/llm/identity.go`

```go
const IdentityVersion = "matrix-identity-v1"
const IdentityPreamble = "You are Matrix — a cognition+UX layer running on the Paxeer Network..."

func InjectIdentity(messages []interpreter.Message) []interpreter.Message
func IdentityModelDigestSuffix(cfg Config) string  // "+identity=matrix-identity-v1"
```

## Identity Guardrails — `neo/internal/agent/identity.go`

```go
func scrubIdentity(name, text string) (string, bool)  // Rewrites model self-IDs
func identityReanchorNudge(name string) string         // Correction guidance
```

Banned self-identity tokens: chatgpt, gpt-*, grok, claude, gemini, bard, llama, qwen, kimi, deepseek, mistral, copilot, openai, anthropic, google deepmind, deepmind, moonshot ai, meta ai, xai.

## Lifecycle — `executor/lifecycle/machine.go`

```go
type Machine struct { ... }
func New(intentID string, initial State) (*Machine, error)
func (m *Machine) Apply(env *envelope.Envelope, opts ApplyOpts) (State, *Event, error)
```

States: Drafting → Proposed → Clarifying → Accepted → Executing → Completed | Failed | Cancelled.

Transition table: `MessageKindTransition` maps envelope kinds to target states.

## Cortex Keys — `cortex/keys/keys.go`

Namespace prefixes (all under single Pebble DB):
- `m/` — Memory heads (id:16 ULID)
- `mv/` — Memory versions (id:16/v/version:8)
- `e/from/` — Edge forward (src:16/edge:1/dst:16)
- `e/to/` — Edge reverse (dst:16/edge:1/src:16)
- `j/` — Journal entries (seq:8)
- `snap/` — Snapshot manifests (seq:8)
- `idx/type/` — Type index (type:1/created:8/id:16)
- `idx/tag/` — Tag index (tag_hash:8/created:8/id:16)
- `salience/` — Salience scores (id:16)
- `vec/meta/` — Vector metadata (id:16)
- `meta/` — Store metadata (journal_head, salience_weights, compile_cache/, goal_state/, session_head/)
- `accum/` — MMR accumulator nodes
- `idx/smt/` — SMT node cache (ns/depth:2/path:32)

All numeric: big-endian fixed-width. IDs: 16-byte binary ULIDs. Strings: 1-byte length-prefixed.

## .kvx Codegraph Format

Generated files in `graph/` directory. Structure:
```
# GENERATED gen=codegraph do-not-edit merkle=b3:<root_hash>

NODE id=<package_path> kind=package
  digest=b3:<hash> file=<relative_path> range=<start>:<end> lang=go exported=true
EDGES
  contains=<child_packages>
  imports=<imported_packages>
  ^contains=<parent_packages>
  ^references=<packages_that_reference_this>
```

Each .kvx file mirrors a Go package. The `digest` field is per-node (function, type, variable). `sig` field contains the Go signature. `byte_range` maps to source file positions.

**Navigation rule:** `graph/matrix/codegraph/selfmodel.kvx` → actual code at `codegraph/selfmodel/selfmodel.go`.

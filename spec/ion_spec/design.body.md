# ION — The Architecture

## Mission

Ion exists because every agent framework in production today is, at its core, a loop. The LLM generates tokens. A harness reads those tokens, identifies tool calls, executes them, and feeds the results back into the context window. Round and round, until the budget runs out or the user stops asking questions.

This is not intelligence. This is autocomplete with a terminal attached.

The gap is not in what the models can do. GPT-4-class models can reason about complex systems, write production code, synthesize information across domains, and hold coherent conversations over hundreds of turns. The bottleneck is not capability. The bottleneck is architecture.

Ion is the architecture that fills the gap. It integrates epistemic depth,
durable memory, reusable skills, multi-channel presence, and controlled
extensions, then adds ten capabilities missing from conventional agent loops:
genuine curiosity, emotional state modeling, social awareness, strategic
forgetting, dreams, temporal embodiment, metacognitive confidence monitoring,
repair-after-failure identity, goal generation, and aesthetic judgment.

The fire is already burning in the models. Ion builds the hearth.

---

## The Regression Corpus

Before designing, we name the failures that motivate the design.

**The Autocomplete Trap.** Every agent in production today does not know what it knows. It has no inventory of its own beliefs, no map of what it has verified versus what it has assumed. When it tells you an API returns JSON, it does not know whether this is something it confirmed yesterday or something it is generating because the pattern completion made it likely. It has no provenance on its own claims.

**The Hallucination Problem.** Every token is emitted with the same flat confidence. "The capital of France is Paris" and "The library uses a custom serialization format" leave the model with identical fluency. One is a fact. The other is a fabrication. The agent has no mechanism to distinguish between them.

**The Forgetting Problem.** When a tool call fails, the agent may retry or fall back, but it does not update its model of the world. The next conversation starts from the same place. The mistake is logged in a session database that no one reads. The agent's beliefs are unchanged.

**The Death Problem.** The agent dies the moment the conversation ends. The next invocation is a resurrection with no memory of its prior life. There is no continuous existence, no narrative, no growth.

These are not theoretical problems. They are visible in every agent deployment in production today. Ion solves them structurally — not with prompt engineering, not with longer context windows, but with architecture.

---

## Load-Bearing Ideas

### 1. Assumption Is a Cost Gradient

A model assumes whenever generating is cheaper than retrieving. The only real implementation of "never let the LLM assume" is residency: the truth already in the field when the premise forms. A model cannot assert "I have an OpenAI-compatible API" while the sentence "Neo's external surface is POST /chat on :8081; there is no OpenAI-compatible endpoint" sits in its own context.

This is why the activation composer exists. This is why the self-model is rendered into the stable prefix. This is why the premise ledger is checked before every dispatch.

### 2. Prediction Error Is the Self-Correction Primitive

An agent catches its own mistakes the way any intelligence does: it predicts, observes, and treats mismatch as a first-class event that forces belief revision. When every probe-class action carries a stated expectation, a failed expectation becomes a structural event the loop must answer — and an action with no stateable expectation is, by definition, a guess the core can refuse.

### 3. A Task Is a Structure, Not Prose

Convergence is unmeasurable over narration. Reify the task — goal, subgoals, premises, evidence — and convergence becomes computable: did the last action discharge a subgoal or add evidence? N actions with an unchanged evidence set is non-convergence by measurement.

### 4. Memory Must Dream

Biological brains consolidate and reorganize during sleep. An agent that remembers everything equally is burdened. The Dreamweaver reorganizes memory during idle time, finding patterns across unrelated experiences and generating novel connections. Strategic forgetting lets go of what no longer matters. This is where genuine insight comes from — not from executing tasks, but from letting the mind wander.

### 5. Liveness Is Emergent, Not Performed

Emotional state modulates decision-making — it does not generate sentiment. Curiosity drives real exploration — it does not produce simulated wonder. Dreams reorganize real memories — they do not generate poetic text. Every liveness mechanism produces observable, measurable behavioral changes in the agent's execution — not cosmetic output variations.

---

## The Seven Layers

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     SAFETY CLASSIFICATION LAYER                          │
│  GREEN/YELLOW/RED enforcement · Policy pipeline · Non-negotiables       │
│  Emotional state CANNOT override this layer (SADR-001)                  │
├─────────────────────────────────────────────────────────────────────────┤
│                        PRESENCE LAYER                                    │
│  Morning Brief · Heartbeat · Multi-Channel · Automatrix · SOUL.md       │
├─────────────────────────────────────────────────────────────────────────┤
│                      REFLECTION ENGINE                                   │
│  Cassandra (rate-limited, 72h undo) · Skill Forge ·                     │
│  Dreamweaver (scoped) · Self-Model Evolver · Prediction Reconciler      │
├─────────────────────────────────────────────────────────────────────────┤
│                       ACTION ENGINE                                      │
│  Dual-Rail Executor · Honest Partials · Tool Loop Breaker ·             │
│  Error Recovery Cascade · Trajectory Export                              │
├─────────────────────────────────────────────────────────────────────────┤
│                        INTENT ENGINE                                     │
│  Task Graph · Prediction Dispatch · Evidence Delta ·                     │
│  Premise Ledger (MMR-bound citations) · Goal Generator ·                │
│  Sub-Agent Orchestrator (depth≤2, HMAC auth, no vault inheritance)      │
├─────────────────────────────────────────────────────────────────────────┤
│                        BELIEF ENGINE                                     │
│  Premise Store · Confidence Monitor · Self-Model (immutable core) ·     │
│  9-Type Memory Taxonomy · Merkle-Anchored Journal ·                     │
│  Honeypot Canaries                                                      │
├─────────────────────────────────────────────────────────────────────────┤
│                      PERCEPTION ENGINE                                   │
│  Activation Composer (4+1 tier) · Memory Prefetch · Cross-Session Search│
│  Pager (HNSW) · Emotional State Modulator                               │
├─────────────────────────────────────────────────────────────────────────┤
│                      FOUNDATION LAYER                                    │
│  Vault Encryption (AES-256-GCM envelope) · O1 Protocol ·               │
│  Plugin SDK · Extension Registry · Policy Pipeline (5 layers) ·         │
│  Session Store (SQLite+FTS5) · SSRF Dispatcher                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Data Flow: Standard Agent Turn

```
User message arrives
  │
  ▼
Perception Engine
  ├── Activation Composer: assemble 4+1 tier working set (Pinned + Timeline + Recent + Dreams + Transcript)
  ├── Memory Prefetch: HNSW k=10 + FTS5 + salience scan + relevant premises
  ├── Emotional State: apply decay, evaluate modulation
  └── Circuit Breakers: check frustration/fatigue/resonance thresholds
  │
  ▼
Intent Engine
  ├── Premise Ledger: extract load-bearing assumptions from plan
  ├── Prediction Dispatch: generate expect parameter for each tool call
  └── Task Graph: update with new subgoals and evidence
  │
  ▼
LLM Provider (O1 Protocol)
  ├── Generate response with tool calls
  └── Each tool call carries expect parameter
  │
  ▼
Action Engine
  ├── Policy Pipeline: 5-layer evaluation (Sandbox → Profile → Provider → Sender → Group)
  ├── Dual-Rail Selection: conversational (reversible) or rigorous (consequential)
  ├── Tool Execution: with timeout, error handling, prediction comparison
  └── Error Recovery: 6-layer cascade if failure
  │
  ▼
Reflection Engine
  ├── Prediction Reconciler: compare expect vs actual, update premise ledger
  ├── Cassandra: check for doubt signals, edit prior messages if needed
  ├── Self-Model Evolver: update capability reliability, failure patterns
  └── Writeback Consolidator: promote learnings to Cortex
  │
  ▼
Belief Engine
  ├── Cortex: store new memories with type, provenance, MMR hash
  ├── Confidence Monitor: annotate low-confidence segments
  └── Strategic Forgetting: decay old memories, protect load-bearing ones
  │
  ▼
Response to user
```

---

## Data Flow: Idle-Time Cycle

```
Heartbeat tick (60s)
  │
  ▼
Check cron jobs → execute due tasks
  │
  ▼
Check Automatrix queue → execute implied opportunities (conversational rail only)
  │
  ▼
Check sub-agent completions → synthesize results
  │
  ▼
Update emotional state (decay toward baseline)
  │
  ▼
If idle:
  ├── Curiosity Engine
  │   ├── Scan for fringe nodes (connection density <15th percentile)
  │   ├── Scan for contradictions (entailment score <0.3)
  │   ├── Scan for gaps (recurring unanswerable questions ≥3)
  │   ├── Weight targets by recency × frequency × centrality
  │   └── Explore top targets (max 20 tool calls, conversational rail, SSRF dispatcher)
  │
  └── Dreamweaver
      ├── Select memory cluster (recently accessed, not recently reorganized)
      ├── Find cross-domain patterns (structural similarity)
      ├── Generate novel connections (candidate Belief memories, min 3 supporting)
      ├── Reorganize graph (strengthen/weaken/merge)
      └── Surface insights (tagged as dream-derived)
```

---

## Security Boundaries

```
┌──────────────────────────────────────────────────────────┐
│                    EXTERNAL ZONE                          │
│  LLM Providers · Web · External MCP Servers              │
└────────────────────────────┬─────────────────────────────┘
                             │ O1 Protocol (normalized)
                             │ SSRF Dispatcher (blocked IPs)
                             ▼
┌──────────────────────────────────────────────────────────┐
│                    POLICY ZONE                            │
│  5-Layer Pipeline: Sandbox → Profile → Provider →        │
│  Sender → Group                                          │
│  GREEN/YELLOW/RED enforcement                            │
└────────────────────────────┬─────────────────────────────┘
                             │ Policy-approved tool calls only
                             ▼
┌──────────────────────────────────────────────────────────┐
│                    AGENT ZONE                             │
│  Agent Loop · Engines · Emotional State · Self-Model     │
│  ┌──────────────────────────────────────────────┐        │
│  │            CORTEX ZONE                       │        │
│  │  Journal (append-only) · MMR · SMT · HNSW   │        │
│  │  Vault encryption below hash boundary        │        │
│  └──────────────────────────────────────────────┘        │
└────────────────────────────┬─────────────────────────────┘
                             │ Scoped tool surface (no vault keys)
                             ▼
┌──────────────────────────────────────────────────────────┐
│                    SUB-AGENT ZONE                         │
│  Reduced Self-Model · HMAC-authenticated verbs           │
│  No vault keys · No cross-session memory                 │
│  No rigorous rail · No spawn (unless orchestrator)       │
└──────────────────────────────────────────────────────────┘
```

---

## Non-Goals

- **No MCL signed-walk or value-moving changes** in the conversational rail.
- **No interrupt/stop/continuation rework** — existing mechanisms preserved.
- **No new Cortex memory types** beyond the 9 defined.
- **No client UI beyond the safety dashboard.**
- **No prompt engineering tricks** for liveness — all mechanisms are structural.
- **No hand-written self-model** — always derived from codegraph and execution history.
- **No stubs, mocks, or fakes** in tests except at true external boundaries.

---

## Verification Strategy

Every mechanism is provable on real code paths:

1. **Premise ledger**: A real plan produces a ledger whose premises carry correct provenance, rendered resident, against the real agent loop.
2. **Prediction dispatch**: Expectations ride real dispatches; deterministic mismatches are detected; the meter accumulates across probes of one strategy.
3. **Cassandra**: Edit produces correct delta; original preserved in journal; rate limits enforced; undo restores original.
4. **Emotional state**: Decay math converges to baseline; behavioral modulation changes strategy; safety classification not overridden.
5. **Dreamweaver**: Cycle produces valid Beliefs; Identity/Preference/Constraint untouched; adversarial source triggers quarantine.
6. **Sub-agents**: Spawn with inherited model; HMAC verification; orphan detection and recovery.

No test in this project substitutes a fake for a real code path. The tests ARE the executable specification.

---

*Ion architecture, July 2026.*

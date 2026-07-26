# Theory-Grounded Codebase Analysis

Methodology for analyzing a codebase against a theoretical framework (papers, specs, architecture documents).

## When this applies

- User provides academic papers or design docs and says "now analyze the codebase with this in mind"
- User says "the system that came out of these papers" or "read my papers then look at the code"
- User asks you to trace theoretical claims through implementation
- The codebase is the reference implementation of a published theory

## Methodology

### Phase 1: Load the theory completely

- Extract full text of all papers/specs (web_extract, read_file)
- Read omitted middles — truncated paper analysis is worthless
- Build a concept map: what are the 3-5 core claims?
- Note internal tensions between papers (these are high-signal, not noise)

### Phase 2: Orient to the codebase

- Clone, tree inventory, module count (standard repo-deep-research steps)
- Read ARCHITECTURE.md / HOW_*_WAS_BUILT.md if present
- Identify which modules map to which theoretical claims

### Phase 3: Source-first correspondence analysis

For each core theoretical claim:

| What to read | What to look for |
|---|---|
| The mechanism files (not READMEs) | Does the code implement the theory's mechanism? |
| The invariant/enforcement code | Does the code enforce the theory's constraints? |
| The test files | Do tests verify the theory's predictions? |
| The spec files (.kvx, .mtx) | Does the spec encode the theory as acceptance criteria? |
| The self-model / identity code | Does the system apply its own theory to itself? |

Build an explicit correspondence table:

```
| Paper Concept | Implementation |
|---|---|
| Typed IR (P1 §III) | MCL/ — 10-verb closed vocabulary, EBNF grammar |
| Absence blindness (P2 §III) | premise.go — premise ledger with cited/assumption status |
```

### Phase 4: Evaluate the correspondence

Three possible relationships between theory and code:

1. **Validates**: The code implements what the theory claims. Quote the specific code.
2. **Extends**: The code goes further than the theory. The premise ledger gating dispatch is stronger than Paper 2's Gap-Hunter proposal — say so.
3. **Contradicts**: The code does something the theory says is impossible or inadvisable. Surface it honestly.

### Phase 5: Identify self-referential patterns

The highest-signal finding in theory-grounded analysis is when a system applies its own theory to itself:

- An absence-blindness mitigation system that uses Externalized Presence on its own architecture
- A scaffolding system whose own agents were scaffolded into existence
- A procedural memory system that learns how to learn

These patterns are the strongest evidence that the theory is correct — or the strongest evidence of circular reasoning, depending on your evaluation.

### Phase 6: Synthesize

- Correspondence table as the primary structure
- Strengths section: where theory and code align tightly
- Tensions section: where the theory's claims are in tension with each other or with the code
- The self-constructing loop (if applicable): how the system embodies its own theory
- What's missing: theoretical claims with no implementation, or implementation with no theoretical grounding

## Anti-patterns

- **Mapping without evaluating** — a correspondence table is not analysis. Each row needs a judgment (validates/extends/contradicts).
- **Smoothing tensions** — if two papers make partially contradictory claims, the analysis should show how the code resolves (or amplifies) the tension.
- **Stopping at the architecture diagram** — ARCHITECTURE.md tells you what the system claims to be. The source tells you what it is.
- **Treating spec status as truth** — `status = "done"` in a .kvx means someone marked it done. Verify with tests and implementation.

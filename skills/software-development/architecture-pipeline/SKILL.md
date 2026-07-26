---
name: architecture-pipeline
description: "Multi-phase architecture document pipeline: research → architecture → adversarial security review → revision → implementation plan. Uses specialized subagents for each phase. For building hardened technical architecture from scratch."
tags: [architecture, security, subagents, planning, documentation, adversarial-review]
triggers:
  - user asks to design a system architecture from research
  - user asks for a security review of a design document
  - user asks to produce an implementation plan from an architecture
  - user asks to harden a design against attack vectors
  - multi-document research corpus building
---

# Architecture Pipeline

A 5-phase approach for producing hardened technical architectures through iterative refinement, leveraging specialized subagent roles at each stage.

## When to Use

- Constructing a new system architecture from research materials
- Strengthening an existing design prior to implementation
- Generating a security spec and implementation plan from architectural intent
- Any multi-document corpus where each phase builds on the previous

## The Pipeline

```
Phase 1: Research Synthesis
  └→ Read source materials, produce initial architecture document

Phase 2: Genesis Writing
  └→ Expand architecture into detailed specification (chapters, data structures, appendices)

Phase 2.5: Security Hardening  ← THE KEY DIFFERENTIATOR
  └→ Subagent: adversarial security reviewer
  └→ Produces security bible (threat model, attack vectors, defense architecture)
  └→ Integrate findings into architecture revision

Phase 3: Implementation Planning
  └→ Subagent: deep technical planner
  └→ Produces buildable spec (languages, deps, interfaces, concurrency, storage)

Phase 4+: Build (out of scope for this skill)
```

## Phase Execution Guide

### Phase 1-2: Architecture + Genesis Writing

Examine all source materials first. Compose the architecture document and any extended specification chapters directly. Include:
- Go-style pseudocode for all data structures
- Exact struct definitions with field types
- Component diagrams
- Implementation roadmap with phases and dependencies

**Word count targets**: Architecture ~5000 words, each paper part 5500-7000 words.

### Phase 2.5: Adversarial Security Review (Subagent)

**This is the most important phase.** Spawn a subagent with this role profile:

```
Role: Security architect + adversarial red-teamer
Goal: Attack the architecture from every angle
```

The subagent must produce a security specification covering:
1. Threat model (adversary classes, attack surfaces, crown jewels)
2. Attack vectors (full enumeration per component)
3. Defense architecture (preventive → detective → recovery → residual risk)
4. Safety classification (GREEN/YELLOW/RED for every capability)
5. Non-negotiable constraints (expanded with enforcement mechanisms)
6. Cryptographic specification (exact algorithms, key hierarchy, rotation)
7. Deep dives on novel attack surfaces
8. Sub-agent security model (blast radius, lateral movement prevention)
9. Memory poisoning defenses
10. Day Zero checklist (testable requirements before code)
11. Day 100 checklist (requirements before autonomous features)
12. Residual risk register (accept/transfer/mitigate decisions)

**After the security review returns**, produce a revision document that:
- Lists every change from the original architecture
- Shows revised data structures with security annotations
- Specifies revised component constraints
- Adds security gates to the implementation roadmap
- Documents binding architectural decisions (SADRs)

### Phase 3: Implementation Planning (Subagent)

Spawn a subagent with this role profile:

```
Role: Deep technical planner (20+ years systems experience)
Goal: Bridge architectural intent to executable reality
```

The subagent must read ALL prior documents and produce:
1. Language decisions per component (with rationale)
2. Dependency map (exact libraries, versions, maintenance status)
3. Component architecture (Go interfaces, I/O, state management)
4. Concurrency model (goroutine topology, channels, deadlock prevention)
5. Storage architecture (schemas, formats, WAL config, FD budget)
6. Performance budget (p50/p95/p99 per operation)
7. Deployment architecture (bare metal vs container vs serverless per component)
8. Hard technical limitations (what engineering can't solve)
9. Tradeoff analysis (what we gain vs sacrifice per decision)
10. Invisible risks (dependency deprecation, GC pauses, clock skew)
11. Build order (exact sequence with parallelization map)
12. Testing strategy (unit, integration, perf, security, chaos)

## Subagent Prompt Construction

Key principles for subagent prompts in this pipeline:
- Pass ALL prior documents as context (subagents have no conversation memory)
- Specify exact file paths for input AND output
- Include the full section outline in the prompt
- Set word count targets (security: 8000-12000, implementation: 10000-15000)
- Demand specific, technical, uncompromising output
- Tell the subagent to "name exact libraries, exact versions, exact interfaces"

## Corpus Tracking

Maintain a running corpus summary:

```
Document Name — N words — Role in pipeline
```

After each phase, print the full corpus with word counts. This gives the user visibility into the growing knowledge base.

## Support Files

- `references/subagent-prompt-templates.md` — Battle-tested prompt templates for the security reviewer and technical planner subagents, plus output verification checklist.

## Pitfalls

- **Don't skip the security review.** Every architecture has blind spots. The adversarial subagent finds them.
- **Don't let the security reviewer be polite.** The prompt must say "BRUTAL", "assume the worst", "assume every component will be targeted."
- **Don't produce the implementation plan before the security revision.** The revision changes data structures, adds constraints, and inserts gates that the planner must incorporate.
- **Don't use the same subagent role for security and planning.** They require fundamentally different mindsets (adversarial vs constructive).
- **Don't forget the revision document.** The security review alone isn't enough — you must integrate the findings back into the architecture.
- **Don't let subagents reference files they haven't read.** Always pass explicit file paths and verify the output references the right concepts.

---
name: technical-architecture-proposal
description: "Produce architecture proposals for adding capabilities to existing codebases — systematic codebase exploration, seam-finding, integration-point mapping, grounded recommendations."
version: 1.0.0
author: Neo
license: MIT
platforms: [linux, macos, windows]
metadata:
  ion:
    tags: [architecture, proposal, codebase-analysis, design, integration]
    related_skills: [repository-deep-research, source-code-documentation-rewrite, plan]
---

# Technical Architecture Proposal

Produce architecture proposals for adding new capabilities to existing codebases. The core discipline: every recommendation must reference a real interface, file, or pattern discovered in the codebase. No generic hand-waving.

## When to Use

- User asks for an architecture proposal, design doc, or integration plan for a new feature in an existing codebase.
- User asks "how would we add X to this system?"
- User wants a technical plan for extending a codebase they point you at.

## Methodology

### Phase 1: Codebase Exploration (before writing anything)

1. **Map the directory structure.** Use `search_files(target='files')` to get the lay of the land. Identify the module boundaries, package layout, and entry points.

2. **Read the build manifest.** `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, etc. Understand the dependency graph and language version.

3. **Find the core interfaces.** Every system has 3-5 load-bearing interfaces/types that define its architecture. Read them. In Go: look for interface definitions, struct types with many methods, and factory functions. In TS/JS: look for class hierarchies and abstract types.

4. **Trace the request/response flow.** How does user input enter the system, get processed, and produce output? Read the server handler, the processing loop, and the output mechanism. In Neo's case: `POST /chat` -> `session.submit()` -> `session.drive()` -> agent loop -> `Reporter.Say()` -> SSE broker.

5. **Find the seams.** Where does the system have clean extension points? Look for:
   - Plugin/provider interfaces (strategy pattern)
   - Event systems (publish/subscribe, observers)
   - Middleware chains
   - Configuration injection points
   - Synthetic tool registration patterns

6. **Search for existing related code.** Before proposing something new, search for partial implementations, stubs, or references to the capability you're proposing. Use content search for keywords.

### Phase 2: Integration Analysis

1. **Identify the integration surface.** Which existing interfaces does the new capability plug into? Which new packages/modules are needed?

2. **Map the data flow.** Trace how data moves through the existing system and where the new capability intercepts it.

3. **Identify reuse opportunities.** What existing abstractions can the new capability leverage? (e.g., Neo's `Reporter` interface, `broker` event system, `session.interrupt()` mechanism)

4. **Check for conflicts.** Does the new capability conflict with existing behavior? Thread safety, resource contention, breaking changes.

### Phase 3: Proposal Writing

Structure the proposal with these sections (adapt as needed):

```markdown
## 1. Overview
- Goal: one sentence
- Use cases: bullet list
- Current state: what exists today (with file references)

## 2. [Primary Component] (e.g., Voice Input Pipeline)
- Provider/service options with comparison table
- Recommended choice with rationale
- Interface definition (Go interface, TS type, etc.)
- Implementation sketch with real code

## 3. [Secondary Component] (e.g., Voice Output Pipeline)
- Same structure

## 4. Integration with Existing Architecture
- Specific file/component integration points (reference real files)
- New packages/modules needed
- New API endpoints/routes
- Data flow diagram (ASCII or Mermaid)

## 5. Technical Stack Recommendations
- Dependencies to add
- Configuration schema
- Error handling and fallbacks

## 6. Implementation Roadmap
- Phased delivery with time estimates
- Each phase is independently deployable

## 7. Security & Privacy
- Data handling
- Consent/opt-in requirements

## 8. Cost Analysis
- Per-provider cost tables
- Monthly estimates at different usage levels
```

### Writing Rules

- **Reference real files and interfaces.** Every claim about the existing system should point to an actual file path. "The `Reporter` interface in `internal/agent/reporter.go` defines `Say()`, `Status()`, `Delta()`" is good. "The agent has an output interface" is bad.

- **Include concrete code.** Show Go interfaces, type definitions, function signatures, and short implementation sketches. The reader should be able to start implementing from your proposal.

- **Comparison tables for provider/service choices.** Columns: provider name, key metric (latency, cost, quality), Go SDK availability, recommendation.

- **Configuration schema.** Show the actual YAML/JSON/TOML config structure the user would add.

- **ASCII protocol diagrams** for any new wire protocol or message flow.

- **Phased roadmap.** Each phase delivers usable value. Phase 1 is always the simplest useful slice.

- **No marketing language.** "Best-in-class, cutting-edge, revolutionary" are banned. Say "lowest latency" or "cheapest" or "best quality" with the data to back it up.

## Pitfalls

- **Don't propose without reading the code.** A proposal that says "we could use a plugin system" when the codebase already HAS a plugin system is embarrassing. Always explore first.

- **Don't over-architect early phases.** Phase 1 should be the simplest thing that works. Caching, fallbacks, multi-provider support are Phase 2+ concerns.

- **Don't ignore the existing transport.** If the system uses HTTP+SSE, don't propose WebRTC without explaining why SSE is insufficient. If it uses WebSocket, don't propose gRPC without justification.

- **Don't forget error handling.** Every provider call can fail. Show the fallback chain.

- **Don't write a marketing document.** This is a technical implementation plan. The audience is the engineer who will build it.

- **Don't use em dashes.** Use commas, semicolons, or restructure the sentence. Em dashes are a crutch for unclear writing.

## Support Files

- `references/go-architecture-patterns.md` -- Go-specific patterns for interfaces, provider systems, and clean architecture.

---
name: docs-sync
description: Sync documentation against source code — systematic discrepancy detection and bulk rewrite of reference docs to match current implementations.
tags: [documentation, codebase, sync, reference-docs, api-docs, discrepancy-detection]
triggers:
  - "rewrite docs from source"
  - "update docs to match code"
  - "sync documentation"
  - "docs are out of date"
  - "regenerate reference docs"
  - "documentation drift"
---

# Docs Sync

Synchronize reference documentation with current source code. Apply when docs have diverged from implementation and require systematic correction.

## When to load

- User asks to rewrite/update documentation to match source code
- Docs reference old struct fields, removed functions, or missing features
- Multiple doc pages need bulk correction against a shared codebase
- User says "docs are wrong" or "update docs from source"

## Core workflow

### Phase 1: Inventory both sides

Examine ALL source files and ALL existing doc files before writing anything. Use parallel reads for independent files. You need the complete picture before you can diff.

**Source inventory** — for each source file, extract:
- Struct/type definitions with field names, types, and tags
- Function signatures (exported only, unless unexported are documented)
- Constants and enums with values
- Package-level doc comments (they often contain design decisions)
- Interface definitions and implementations

**Doc inventory** — for each doc page, extract:
- Current struct/field/function claims
- Code examples (are they still valid?)
- Tables of constants/enums (complete? values correct?)
- "Design decisions" sections (do they match current source?)
- Cross-references between doc pages

### Phase 2: Identify discrepancies

Systematic comparison. Common drift patterns (check all):

1. **Struct field drift** — field added/removed/renamed/retyped
2. **Missing journal/entry kinds** — new phases add kinds that docs don't list
3. **Missing enum values** — new types, edge types, verbs, etc.
4. **Wrong function signatures** — parameters changed, return values added
5. **Stale code examples** — structs in examples have old field names
6. **Missing features** — new phases landed but docs don't cover them
7. **Wrong cross-references** — doc A says "see X" but X moved or renamed
8. **Outdated namespace tables** — new key prefixes, changed key shapes
9. **Wrong invariant claims** — invariants relaxed or strengthened since docs were written
10. **Missing error types** — new errors added that aren't in error reference tables

### Phase 3: Rewrite

Write all files. Do NOT patch individual fields — rewrite whole files to ensure internal consistency. A doc page with 3 corrected fields but 2 stale paragraphs is worse than a clean rewrite.

## Pitfalls

### Partial reads cause missed discrepancies
**Always read the FULL source file**, not just the first 100 lines. Many critical definitions (struct fields, constants, error vars) live in the middle or end of files. Use `offset` and `limit` to page through large files. If a file is truncated, continue reading from the offset.

### Don't trust the README as source of truth
READMEs often lag behind actual code. The code is truth. If README says one thing and source says another, source wins. Document the discrepancy if it's a design decision.

### Batch reads, serialize writes
Read all source files in parallel (they're independent). Read all existing docs in parallel. Then write docs sequentially — each write depends on having the complete source picture.

### Preserve doc structure, fix content
Keep the existing page structure (headings, section order, code block placement) unless the source has fundamentally changed the architecture. Users expect docs to be recognizable after a sync, not reorganized.

### Check ALL enum/const tables completely
When source defines 14 edge types and the doc lists 6, you must list all 14. Don't just add the missing ones — verify every existing entry's byte value is correct too.

### Verify function parameter names match
Go function signatures in docs often use different parameter names than the actual source. Match the source names unless the doc names are clearer (and then note both).

## Reference files

- `references/cortex-docs-sync-examples.md` — concrete discrepancy patterns from a 13-page Go-to-MDX sync. Use as a checklist of what drift looks like (struct field drift, missing journal kinds, wrong formulas, stale enum tables).

## Quality checklist (before finishing)

- [ ] Every exported struct in source has a corresponding doc section
- [ ] Every enum/const table in docs matches source values exactly
- [ ] Every code example uses current field names and types
- [ ] Every "Design decisions" claim is verified against source comments
- [ ] Cross-references between doc pages still resolve
- [ ] Error reference tables are complete
- [ ] Repository layout tree matches actual file structure
- [ ] No em dashes in the output (use commas or semicolons instead)

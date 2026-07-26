# Batch Rewrite Workflow

Detailed workflow for rewriting 10+ documentation files from source code in a single session.

## Phase 1: Source reconnaissance (parallel)

Read ALL source files in one batch. Do not serialize independent reads. For a 14-file rewrite, this means one assistant turn with 14+ read_file calls.

Identify the source file categories:
- Primary source code (Go modules, JSON manifests, Python scripts)
- Index/catalog files (INDEX.md, INDEX.json, PORT_MANIFEST.json)
- Configuration files (agent manifests, rules)

Extract and mentally index:
- Key facts, numbers, and counts
- Structural patterns (schema versions, field names, hierarchies)
- Relationships between files (which references which)

## Phase 2: Existing doc reconnaissance (parallel)

Read ALL existing documentation files in one batch. Note:
- Frontmatter format (title, description fields)
- Section hierarchy (## headings, ### subheadings)
- Table conventions (column names, alignment)
- Mermaid diagram usage
- MDX component usage (Card, Steps, Note, Warning, Tip, Columns)

## Phase 3: Systematic rewrite (sequential)

Write files one at a time. Order matters:
1. Start with files that establish the vocabulary (skills index, agent manifests)
2. Then files that reference that vocabulary (standards, developer pages)
3. End with files that cross-reference others

For each file:
- Preserve the frontmatter structure
- Update content to match current source
- Maintain consistent terminology across files
- Use the same table formatting conventions

## Phase 4: Verification (parallel reads)

After all writes complete, run verification:
1. search_files for forbidden patterns (e.g., [—–] for em dashes)
2. Spot-check a few files for accuracy against source
3. Verify frontmatter consistency

## Timing notes

For a 14-file rewrite:
- Phase 1: 1 turn (parallel reads)
- Phase 2: 1 turn (parallel reads)
- Phase 3: 14 turns (one per file write)
- Phase 4: 1 turn (parallel searches)

Total: ~17 turns instead of 28+ if reads were serialized.

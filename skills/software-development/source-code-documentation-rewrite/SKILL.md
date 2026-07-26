---
name: source-code-documentation-rewrite
description: Systematically rewrite documentation pages from source code truth — read the implementation, compare against existing docs, and produce accurate reference documentation that reflects the actual codebase.
triggers:
  - rewrite docs from source
  - update documentation to match code
  - regenerate reference docs
  - docs are stale or outdated
  - documentation doesn't match the code
---

# Source-Code Documentation Rewrite

Rewrite documentation pages by reading the actual source code, not by trusting the existing docs. The existing docs are a starting point for structure; the source code is the source of truth for content.

## Workflow

### 1. Explore the file structure

Find all source files and all existing doc files. Map the correspondence:
- Which source files feed which doc pages?
- Are there source files with no doc coverage?
- Are there doc pages with no current source backing?

Use `search_files(target='files')` to discover the tree.

### 2. Read architecture docs FIRST

Before diving into source code, read the project's own documentation:
- **README.md** at the repo root (overall framing, module table, architecture diagram)
- **README.md** in each module directory (module-specific layout, API, invariants)
- **docs/INDEX.md** files (the project's own internal docs index)
- **docs/<module>-docs/INDEX.md** (module-level doc index with descriptions)

These files contain the author's mental model and tell you which source files matter most. Reading them first prevents wasted time on peripheral code.

### 3. Read source code in batches

Read the key implementation files first. Prioritize:
1. Entry points (main.go, server.go, cmd/)
2. Core types and interfaces (types.go, interfaces)
3. Implementation files referenced by existing docs
4. Config and wiring files

Batch independent reads (up to 10 at a time) to minimize round-trips. For large files, read the first 500 lines, then continue if needed.

### 4. Read existing docs

Read all existing doc pages to understand:
- Current structure and section headings
- What's stale, missing, or wrong
- The doc format conventions (frontmatter, tables, code blocks)

### 5. Rewrite in batches

Write docs in groups of 4-7 files per batch. For each file:
- Preserve the overall structure if it's sound
- Update every claim to match the source code
- Add missing features, types, methods, error codes
- Remove stale information about deleted/replaced features
- Use tables for type definitions, method signatures, error codes
- Include source file paths for traceability

### 6. Verify completeness

After writing, do a final file listing to confirm all target docs were written. Count files to make sure none were skipped.

## Format conventions

- Use `---` YAML frontmatter with `title` and `description`
- Start with `**Source file(s):**` pointing to the implementation
- Use `## Design decisions` sections to explain the *why*
- Use tables for types, methods, routes, error codes, config fields
- Include code blocks for key type definitions and function signatures
- Use `## Modifying <thing>` tables at the end of each page
- End with error code tables where applicable

## Pitfalls

- **Do not trust the existing docs as truth.** They may describe features that were deleted, renamed, or replaced. Always verify against source.
- **Do not invent features.** If the source code doesn't implement something, don't document it as existing. Use "(reserved)" or omit it.
- **Watch for phased/incremental implementations.** Codebases with migration files or phase comments may have partially-implemented features. Document what exists, note what's planned.
- **Batch reads aggressively.** Reading 10 files in parallel is much faster than 10 sequential reads. Group independent files.
- **Write docs even when you can't read every source file.** Prioritize the files the existing docs reference. You can always note "see source for details" for peripheral files.
- **Preserve doc filenames exactly.** If the existing file is `service-entrypoints-api-server-docs-and-schema-migrations.mdx`, keep that name even if it's long. Changing filenames breaks cross-references.

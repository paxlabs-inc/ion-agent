---
name: documentation-sync
description: Rewrite documentation files to match their source-of-truth code. Use when docs are stale, source code changed, or user asks to update docs based on current code.
triggers:
  - rewrite documentation
  - update docs from source
  - sync docs with code
  - regenerate documentation
  - docs are stale
---

# Documentation Sync

Bring documentation files into alignment with their source-of-truth code. Apply this when docs have fallen behind, the source code has shifted significantly, or the user requests a documentation update/rewrite based on current code.

## Trigger

- The user asks to rewrite, update, or sync documentation based on source code
- Documentation is known to be stale or outdated after code changes
- Source code has changed significantly and docs need to reflect the current state
- Multiple documentation files need updating as a batch

## Workflow

### 1. Read all source files in parallel

Batch all independent source file reads into a single parallel call. This is the most efficient way to understand the current state of the system before writing anything.

- Identify all source files that the documentation covers
- Read them all in one batch (parallel tool calls)
- Extract key facts, structures, APIs, and patterns
- Note any discrepancies with existing documentation

### 2. Read existing documentation

Read the existing documentation files (also in parallel) to understand:
- Current structure and format (frontmatter, sections, tables)
- Section organization and hierarchy
- Style, tone, and formatting conventions
- What is still accurate vs. what needs updating

### 3. Systematic rewrite

Rewrite each file one at a time:
- Start with the most foundational files (the ones others reference)
- Use accurate, source-backed content: every claim traceable to code
- Maintain consistent style across all files
- Preserve the existing structure where it works
- Update sections that are stale or inaccurate
- Use tables for structured data, code blocks for commands

### 4. Verification pass

After all files are written:
- Use `search_files` to check for forbidden patterns across all output files (e.g., em dashes, inconsistent formatting, style violations)
- Cross-check key facts against source code
- Verify frontmatter is correct and consistent
- Check for consistency across files (terminology, formatting)

## Pitfalls

- **Do not invent content.** Every claim should be traceable to source code. If you cannot find it in the source, do not write it.
- **Do not skip the verification pass.** It catches inconsistencies and formatting issues that are easy to miss during writing.
- **Do not write all files in one batch.** Write them sequentially to maintain quality and consistency.
- **Do not ignore existing structure.** Preserve what works, update what does not. Do not restructure unnecessarily.
- **Do not mix source-backed facts with assumptions.** Clearly distinguish between what is in the code and what you are inferring.
- **Do not read source files sequentially when they are independent.** Parallel reads cut round-trips dramatically on large batches.

## Style Guidelines

- Use source-backed content (traceable to code)
- Maintain consistent formatting across all files
- Use clear, technical language
- Avoid marketing or promotional language
- Use tables for structured data (APIs, properties, methods)
- Use code blocks for commands, examples, and code snippets
- Preserve frontmatter conventions from existing files

## Verification Checklist

- [ ] All source files read and understood
- [ ] Existing documentation structure preserved where appropriate
- [ ] All stale content updated with source-backed facts
- [ ] No forbidden patterns found (search across all output files)
- [ ] Frontmatter is correct and consistent
- [ ] Cross-file consistency verified (terminology, formatting)
- [ ] Every claim traceable to source code

meta {
  name: "source-code-documentation-rewrite"
  version: "1.0.0"
  summary: "Systematically rewrite documentation pages from source code truth — read implementation, compare against existing docs, produce accurate reference documentation."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "rewrite docs"
  keywords: "update docs"
  keywords: "regenerate docs"
  keywords: "documentation rewrite"
  keywords: "docs from source"
  keywords: "stale docs"
  intents: "rewrite_documentation"
  intents: "update_docs_from_source"
  intents: "regenerate_reference_docs"
  patterns: "(rewrite|update|regenerate) .*(docs|documentation) .*(source|code)"
  patterns: "docs .*(stale|outdated|wrong|don't match)"
  patterns: "documentation .*(doesn't match|drifted)"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "terminal"
    required: false
  }
}

provides {
  capabilities: "documentation_rewrite"
  output_types: ".md"
  output_types: ".mdx"
}

actions {
  id: "rewrite_docs"
  description: "Rewrite documentation by reading actual source code"
  trigger_phrases: "rewrite docs from source"
  trigger_phrases: "update docs to match code"
  trigger_phrases: "regenerate reference docs"
    rules {
      text: "Do NOT trust existing docs as truth — they may describe deleted, renamed, or replaced features. Always verify against source."
      priority: CRITICAL
    }
    rules {
      text: "Do NOT invent features — if source doesn't implement something, document it as (reserved) or omit"
      priority: CRITICAL
    }
    rules {
      text: "Read architecture docs FIRST (README.md, docs/INDEX.md) to understand author's mental model before diving into source"
      priority: HIGH
    }
    rules {
      text: "Batch reads aggressively — up to 10 independent files at a time to minimize round-trips"
      priority: HIGH
    }
    rules {
      text: "Write docs in groups of 4-7 files per batch"
      priority: HIGH
    }
    rules {
      text: "Preserve doc filenames exactly — changing names breaks cross-references"
      priority: NORMAL
    }
    rules {
      text: "Use tables for type definitions, method signatures, error codes, config fields"
      priority: NORMAL
    }
    data {
      key: "format_conventions"
      list_value {
        items {
          string_value: "YAML frontmatter with title and description"
        }
        items {
          string_value: "Start with **Source file(s):** pointing to implementation"
        }
        items {
          string_value: "Use ## Design decisions sections for the why"
        }
        items {
          string_value: "Use tables for types, methods, routes, error codes"
        }
        items {
          string_value: "Include code blocks for key type definitions"
        }
        items {
          string_value: "Use ## Modifying <thing> tables at end of each page"
        }
      }
    }
    data {
      key: "priority_order"
      list_value {
        items {
          string_value: "1. Entry points (main.go, server.go, cmd/)"
        }
        items {
          string_value: "2. Core types and interfaces"
        }
        items {
          string_value: "3. Implementation files referenced by existing docs"
        }
        items {
          string_value: "4. Config and wiring files"
        }
      }
    }
    examples {
      label: "batch read workflow"
      language: "text"
      code: "# Phase 1: Explore file structure\nsearch_files(target='files')\n# Phase 2: Read architecture docs\nread_file(\"README.md\")\n# Phase 3: Read source in batches (10 at a time)\n# Phase 4: Read existing docs\n# Phase 5: Rewrite in batches of 4-7 files"
    }
}
actions {
  id: "verify_completeness"
  description: "Verify all target docs were written after rewrite"
  trigger_phrases: "verify docs completeness"
  trigger_phrases: "check docs coverage"
    rules {
      text: "Do a final file listing to confirm all target docs were written — count files to make sure none were skipped"
      priority: HIGH
    }
    rules {
      text: "Watch for phased/incremental implementations — document what exists, note what's planned"
      priority: NORMAL
    }
}

guardrails {
  text: "Source code is always truth — never trust docs over source"
  scope: ALWAYS
}

guardrails {
  text: "Never invent features not in the source"
  scope: ALWAYS
}

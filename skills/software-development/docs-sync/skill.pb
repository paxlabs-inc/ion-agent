meta {
  name: "docs-sync"
  version: "1.0.0"
  summary: "Sync documentation against source code — systematic discrepancy detection and bulk rewrite."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "docs sync"
  keywords: "documentation sync"
  keywords: "update docs"
  keywords: "rewrite docs"
  keywords: "docs drift"
  keywords: "reference docs"
  intents: "sync_documentation"
  intents: "rewrite_docs"
  intents: "update_docs_from_source"
  patterns: "(rewrite|update|sync) .*(docs|documentation) .*(source|code)"
  patterns: "docs .*(out of date|stale|wrong|drift)"
  patterns: "regenerate .*(reference|api) docs"
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
  capabilities: "documentation_sync"
  output_types: ".md"
  output_types: ".mdx"
}

actions {
  id: "docs_sync"
  description: "Synchronize reference documentation with current source code"
  trigger_phrases: "sync docs"
  trigger_phrases: "update docs from source"
  trigger_phrases: "docs are wrong"
  trigger_phrases: "rewrite docs from source"
    rules {
      text: "Always read the FULL source file — many critical definitions live in the middle or end"
      priority: CRITICAL
    }
    rules {
      text: "The code is truth — if README says one thing and source says another, source wins"
      priority: CRITICAL
    }
    rules {
      text: "Rewrite whole files, don't patch individual fields — a doc with 3 corrected fields but 2 stale paragraphs is worse than a clean rewrite"
      priority: HIGH
    }
    rules {
      text: "Check ALL enum/const tables completely — when source defines 14 edge types and doc lists 6, list all 14"
      priority: HIGH
    }
    rules {
      text: "Preserve existing page structure (headings, section order) unless source fundamentally changed architecture"
      priority: HIGH
    }
    rules {
      text: "Batch reads for independent files (parallel), serialize writes"
      priority: NORMAL
    }
    rules {
      text: "Verify function parameter names match source — Go signatures often differ from docs"
      priority: NORMAL
    }
    data {
      key: "drift_patterns"
      list_value {
        items {
          string_value: "Struct field drift (added/removed/renamed/retyped)"
        }
        items {
          string_value: "Missing journal/entry kinds"
        }
        items {
          string_value: "Missing enum values"
        }
        items {
          string_value: "Wrong function signatures"
        }
        items {
          string_value: "Stale code examples"
        }
        items {
          string_value: "Missing features"
        }
        items {
          string_value: "Wrong cross-references"
        }
        items {
          string_value: "Outdated namespace tables"
        }
        items {
          string_value: "Wrong invariant claims"
        }
        items {
          string_value: "Missing error types"
        }
      }
    }
    examples {
      label: "inventory source and docs"
      language: "bash"
      code: "# Source inventory: extract struct definitions, function signatures, constants\ngrep -n 'type .*struct' src/*.go\ngrep -n 'func ' src/*.go | grep -v '^.*func init'\n# Doc inventory: check each doc page against source"
    }
}
actions {
  id: "docs_quality_check"
  description: "Run quality checklist on synced documentation"
  trigger_phrases: "check docs quality"
  trigger_phrases: "verify docs"
  trigger_phrases: "docs checklist"
    rules {
      text: "Every exported struct in source must have a corresponding doc section"
      priority: HIGH
    }
    rules {
      text: "Every enum/const table in docs must match source values exactly"
      priority: HIGH
    }
    rules {
      text: "Every code example must use current field names and types"
      priority: NORMAL
    }
    rules {
      text: "No em dashes in output — use commas or semicolons instead"
      priority: NORMAL
    }
    data {
      key: "quality_checklist"
      list_value {
        items {
          string_value: "Every exported struct has doc section"
        }
        items {
          string_value: "Every enum/const table matches source"
        }
        items {
          string_value: "Code examples use current field names"
        }
        items {
          string_value: "Design decisions verified against source comments"
        }
        items {
          string_value: "Cross-references still resolve"
        }
        items {
          string_value: "Error reference tables complete"
        }
        items {
          string_value: "Repository layout tree matches actual structure"
        }
      }
    }
}

guardrails {
  text: "Source code is always truth — never trust docs over source"
  scope: ALWAYS
}

guardrails {
  text: "Read full source files — never truncate or assume"
  scope: ALWAYS
}

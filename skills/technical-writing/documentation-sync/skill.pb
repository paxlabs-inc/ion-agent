meta {
  name: "documentation-sync"
  version: "1.0.0"
  summary: "Rewrite documentation files to match their source-of-truth code"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "documentation"
  keywords: "docs"
  keywords: "sync docs"
  keywords: "update docs"
  keywords: "stale docs"
  keywords: "rewrite docs"
  intents: "sync_documentation"
  intents: "update_docs"
  intents: "rewrite_docs"
  intents: "regenerate_docs"
  patterns: "(rewrite|update|sync|regenerate) .*(documentation|docs)"
  patterns: "docs are (stale|outdated|old)"
  patterns: "(documentation|docs) .*(out of date|stale|behind)"
}

requires {
  tools {
    name: "read_file"
    required: true
  }
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "search_files"
    required: true
  }
  tools {
    name: "terminal"
    required: false
  }
}

provides {
  capabilities: "documentation_sync"
  capabilities: "documentation_rewrite"
  output_types: ".md"
}

actions {
  id: "read_sources"
  description: "Read all source files in parallel to understand current system state"
  trigger_phrases: "read source code"
  trigger_phrases: "analyze source files"
    rules {
      text: "Batch all independent source file reads into a single parallel call — most efficient approach"
      priority: CRITICAL
    }
    rules {
      text: "Extract key facts, structures, APIs, and patterns from source"
      priority: HIGH
    }
    rules {
      text: "Note discrepancies with existing documentation"
      priority: HIGH
    }
    rules {
      text: "Do not read source files sequentially when they are independent"
      priority: NORMAL
    }
}
actions {
  id: "read_docs"
  description: "Read existing documentation to understand current structure and style"
  trigger_phrases: "read existing docs"
  trigger_phrases: "check current documentation"
    rules {
      text: "Understand current structure: frontmatter, sections, tables, formatting conventions"
      priority: HIGH
    }
    rules {
      text: "Identify what is still accurate vs. what needs updating"
      priority: HIGH
    }
    rules {
      text: "Read existing docs in parallel for efficiency"
      priority: NORMAL
    }
}
actions {
  id: "rewrite"
  description: "Systematically rewrite documentation files one at a time"
  trigger_phrases: "rewrite documentation"
  trigger_phrases: "update docs"
  trigger_phrases: "sync docs with code"
    rules {
      text: "Do not invent content — every claim should be traceable to source code"
      priority: CRITICAL
    }
    rules {
      text: "Start with most foundational files (the ones others reference)"
      priority: HIGH
    }
    rules {
      text: "Maintain consistent style across all files"
      priority: HIGH
    }
    rules {
      text: "Preserve existing structure where it works, update what doesn't"
      priority: HIGH
    }
    rules {
      text: "Use tables for structured data, code blocks for commands"
      priority: NORMAL
    }
    rules {
      text: "Write files sequentially — do not write all files in one batch"
      priority: NORMAL
    }
}
actions {
  id: "verify"
  description: "Verification pass after all files are written"
  trigger_phrases: "verify documentation"
  trigger_phrases: "check docs consistency"
    rules {
      text: "Do not skip the verification pass — it catches inconsistencies easy to miss during writing"
      priority: CRITICAL
    }
    rules {
      text: "Use search_files to check for forbidden patterns across all output files"
      priority: HIGH
    }
    rules {
      text: "Cross-check key facts against source code"
      priority: HIGH
    }
    rules {
      text: "Verify frontmatter is correct and consistent across files"
      priority: HIGH
    }
    rules {
      text: "Check cross-file consistency: terminology, formatting"
      priority: NORMAL
    }
    data {
      key: "verification_checklist"
      list_value {
        items {
          string_value: "All source files read and understood"
        }
        items {
          string_value: "Existing documentation structure preserved where appropriate"
        }
        items {
          string_value: "All stale content updated with source-backed facts"
        }
        items {
          string_value: "No forbidden patterns found (search across all output files)"
        }
        items {
          string_value: "Frontmatter is correct and consistent"
        }
        items {
          string_value: "Cross-file consistency verified (terminology, formatting)"
        }
        items {
          string_value: "Every claim traceable to source code"
        }
      }
    }
}

guardrails {
  text: "Do not invent content — every claim must be traceable to source code"
  scope: ALWAYS
}

guardrails {
  text: "Do not skip the verification pass"
  scope: ALWAYS
}

guardrails {
  text: "Do not mix source-backed facts with assumptions"
  scope: ALWAYS
}

guardrails {
  text: "Write files sequentially, not all in one batch"
  scope: WRITE_OPS
}

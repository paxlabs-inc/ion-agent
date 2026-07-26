meta {
  name: "academic-writing"
  version: "1.0.0"
  summary: "Write academic papers with enforced style constraints and verified real citations"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "academic writing"
  keywords: "research paper"
  keywords: "style constraints"
  keywords: "banned words"
  keywords: "verified citations"
  keywords: "IEEE"
  intents: "write_academic"
  intents: "enforce_style"
  intents: "verify_citations"
  patterns: "(write|draft) .*(paper|report) .*(style|constraint|academic)"
  patterns: "(banned|forbidden) .*(words|phrases)"
  patterns: "verified .*(citations|references)"
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
  capabilities: "academic_writing"
  capabilities: "style_enforcement"
  capabilities: "citation_verification"
  capabilities: "em_dash_replacement"
  output_types: ".txt"
  output_types: ".md"
}

actions {
  id: "gather_citations"
  description: "Search for genuine arXiv IDs, DOIs, or official URLs before writing"
  trigger_phrases: "find citations"
  trigger_phrases: "search references"
  trigger_phrases: "gather citations"
    rules {
      text: "Never fabricate citation IDs — search for genuine arXiv IDs, DOIs, or official URLs"
      priority: CRITICAL
    }
    rules {
      text: "Batch independent citation searches in parallel"
      priority: HIGH
    }
    rules {
      text: "Confirm each ID resolves to the intended paper on arxiv.org"
      priority: HIGH
    }
    rules {
      text: "Maintain a running reference list: authors, title, venue, year, ID"
      priority: NORMAL
    }
    data {
      key: "search_pattern"
      string_value: "arxiv \"exact paper title\" author year arXiv ID"
    }
}
actions {
  id: "write_with_constraints"
  description: "Write with embedded style constraints treated as hard rules"
  trigger_phrases: "write paper"
  trigger_phrases: "draft with constraints"
  trigger_phrases: "apply style rules"
    rules {
      text: "Treat user-supplied style rules as hard constraints — keep banned word list active throughout"
      priority: CRITICAL
    }
    rules {
      text: "For em dashes: use commas, hyphens (-), or parentheses"
      priority: CRITICAL
    }
    rules {
      text: "For 'we' voice: we argue, we show, we propose, we present"
      priority: HIGH
    }
    rules {
      text: "Draft all sections, then verify — don't verify mid-draft"
      priority: HIGH
    }
    rules {
      text: "Include formal definitions and propositions when the topic warrants them"
      priority: NORMAL
    }
    data {
      key: "constraint_types"
      list_value {
        items {
          string_value: "banned_words"
        }
        items {
          string_value: "banned_punctuation"
        }
        items {
          string_value: "required_voice"
        }
        items {
          string_value: "citation_format"
        }
        items {
          string_value: "structure"
        }
        items {
          string_value: "word_count_range"
        }
        items {
          string_value: "claim_precision"
        }
      }
    }
    data {
      key: "em_dash_replacements"
      map_value {
        entries {
          key: "parenthetical_aside"
          string_value: "commas or parentheses"
        }
        entries {
          key: "interrupting_clause"
          string_value: "commas"
        }
        entries {
          key: "range_connection"
          string_value: "hyphen (-)"
        }
        entries {
          key: "dramatic_pause"
          string_value: "rewrite sentence"
        }
      }
    }
}
actions {
  id: "verify_compliance"
  description: "Run automated checks after drafting — never skip"
  trigger_phrases: "verify compliance"
  trigger_phrases: "run checks"
  trigger_phrases: "check style"
    rules {
      text: "Run automated checks before delivering — never skip verification"
      priority: CRITICAL
    }
    rules {
      text: "Check word count, em dashes (UTF-8), and banned words (case-insensitive)"
      priority: HIGH
    }
    rules {
      text: "If violations found: use sed for replacements, patch for complex rewrites"
      priority: HIGH
    }
    rules {
      text: "Re-run full verification after each fix round until zero violations"
      priority: HIGH
    }
    rules {
      text: "Em dash (—) is banned; regular hyphen (-) is fine — verify grep targets correct UTF-8"
      priority: NORMAL
    }
    data {
      key: "check_commands"
      list_value {
        items {
          string_value: "wc -w <file>"
        }
        items {
          string_value: "grep -c '—' <file>"
        }
        items {
          string_value: "grep -i -c -E 'delve|landscape|tapestry|crucial|...' <file>"
        }
      }
    }
    data {
      key: "banned_words_extended"
      list_value {
        items {
          string_value: "delve"
        }
        items {
          string_value: "landscape"
        }
        items {
          string_value: "tapestry"
        }
        items {
          string_value: "crucial"
        }
        items {
          string_value: "furthermore"
        }
        items {
          string_value: "moreover"
        }
        items {
          string_value: "aforementioned"
        }
        items {
          string_value: "notably"
        }
        items {
          string_value: "particularly"
        }
        items {
          string_value: "it is important to note"
        }
        items {
          string_value: "in the realm of"
        }
        items {
          string_value: "in conclusion"
        }
      }
    }
}

guardrails {
  text: "Never fabricate citations — a single fake citation destroys credibility"
  scope: ALWAYS
}

guardrails {
  text: "Always run grep checks after writing, even if careful during drafting"
  scope: ALWAYS
}

guardrails {
  text: "Re-run full check after each fix round until zero violations"
  scope: ALWAYS
}

related {
  name: "humanizer"
}

related {
  name: "research-paper-writing"
}

related {
  name: "arxiv"
}

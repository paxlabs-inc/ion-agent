meta {
  name: "academic-paper-writing"
  version: "1.0.0"
  summary: "Write academic papers as plain text with IEEE structure, anti-AI style rules, and verified citations"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "academic paper"
  keywords: "research paper"
  keywords: "scholarly article"
  keywords: "IEEE"
  keywords: "plain text paper"
  keywords: "anti-AI writing"
  keywords: "citations"
  intents: "write_academic_paper"
  intents: "write_research_paper"
  intents: "scholarly_writing"
  patterns: "(write|draft|create) .*(paper|article|report) .*(academic|research|scholarly)"
  patterns: "IEEE .*(paper|format|structure)"
  patterns: "anti-AI .*(writing|paper|style)"
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
  capabilities: "academic_paper_writing"
  capabilities: "ieee_format"
  capabilities: "citation_verification"
  capabilities: "anti_ai_style"
  output_types: ".txt"
}

actions {
  id: "research_citations"
  description: "Locate genuine arXiv IDs before drafting any prose"
  trigger_phrases: "find citations"
  trigger_phrases: "search arXiv"
  trigger_phrases: "locate references"
    rules {
      text: "Never fabricate citations — every arXiv ID must resolve to a real paper"
      priority: CRITICAL
    }
    rules {
      text: "Use web_search with targeted queries to find genuine arXiv IDs"
      priority: CRITICAL
    }
    rules {
      text: "Confirm each ID follows format: arXiv:YYMM.NNNNN"
      priority: HIGH
    }
    rules {
      text: "Include minimum 15 references in research papers"
      priority: HIGH
    }
    rules {
      text: "Favor arXiv IDs over DOIs for broader accessibility"
      priority: NORMAL
    }
    data {
      key: "id_format"
      string_value: "arXiv:YYMM.NNNNN"
    }
    data {
      key: "min_references"
      int_value: 15
    }
}
actions {
  id: "write_paper"
  description: "Compose paper following IEEE-style structure with voice and style constraints"
  trigger_phrases: "write the paper"
  trigger_phrases: "draft the paper"
  trigger_phrases: "compose the paper"
    rules {
      text: "Use 'we' voice throughout — never 'I' or passive constructions like 'it can be observed'"
      priority: CRITICAL
    }
    rules {
      text: "No em dashes (— or --) — use commas, parentheses, or hyphens"
      priority: CRITICAL
    }
    rules {
      text: "Banned words: delve, landscape, tapestry, crucial, furthermore, moreover, aforementioned, notably, particularly"
      priority: CRITICAL
    }
    rules {
      text: "Banned phrases: 'it is important to note', 'in the realm of', 'in conclusion' (in prose)"
      priority: CRITICAL
    }
    rules {
      text: "Precise claim boundaries — avoid 'may'/'might' unless genuine uncertainty exists"
      priority: HIGH
    }
    rules {
      text: "Include formal definitions and propositions where they strengthen the argument"
      priority: HIGH
    }
    rules {
      text: "IEEE structure: Title, Author, ABSTRACT, Index Terms, numbered Roman numeral sections, REFERENCES"
      priority: NORMAL
    }
    data {
      key: "ieee_structure"
      list_value {
        items {
          string_value: "Title"
        }
        items {
          string_value: "Author, Affiliation"
        }
        items {
          string_value: "ABSTRACT (150-250 words)"
        }
        items {
          string_value: "Index Terms"
        }
        items {
          string_value: "I. INTRODUCTION"
        }
        items {
          string_value: "II-X. Body sections"
        }
        items {
          string_value: "X. CONCLUSION"
        }
        items {
          string_value: "REFERENCES ([1], [2], ...)"
        }
      }
    }
    data {
      key: "banned_words"
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
      }
    }
}
actions {
  id: "verify_paper"
  description: "Run verification checks before delivery — correct ALL violations"
  trigger_phrases: "verify paper"
  trigger_phrases: "check paper"
  trigger_phrases: "run checks"
    rules {
      text: "Run ALL checks before saving: word count, banned words, em dashes, first-person voice, reference count"
      priority: CRITICAL
    }
    rules {
      text: "Word count target: 4000-6000 for research papers"
      priority: HIGH
    }
    rules {
      text: "Banned word grep should return nothing"
      priority: HIGH
    }
    rules {
      text: "Fix violations with sed for word replacements, patch for complex passages"
      priority: NORMAL
    }
    data {
      key: "verification_commands"
      list_value {
        items {
          string_value: "wc -w FILE"
        }
        items {
          string_value: "grep -inE 'delve|landscape|tapestry|crucial|...' FILE"
        }
        items {
          string_value: "grep -cP '\\x{2014}' FILE"
        }
        items {
          string_value: "grep -nP '^I [a-z]|\\. I [a-z]' FILE"
        }
        items {
          string_value: "grep -cP '^\\[\\d+\\]' FILE"
        }
      }
    }
}

guardrails {
  text: "Never fabricate citations — every reference must be verified via web search"
  scope: ALWAYS
}

guardrails {
  text: "Run verification grep before delivering any paper"
  scope: ALWAYS
}

related {
  name: "humanizer"
}

related {
  name: "arxiv"
}

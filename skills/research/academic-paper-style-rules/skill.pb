meta {
  name: "academic-paper-style-rules"
  version: "1.0.0"
  summary: "Write academic papers (.txt, .md) with enforced style constraints: banned words, voice rules, reference verification, IEEE structure."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "academic paper"
  keywords: "style rules"
  keywords: "banned words"
  keywords: "plain text paper"
  keywords: "IEEE paper"
  keywords: "style constraints"
  intents: "write_academic_paper"
  intents: "enforce_style_rules"
  intents: "verify_references"
  intents: "check_compliance"
  patterns: "(write|draft|compose) .*(paper|academic|research).*style"
  patterns: "(banned|prohibited) words.*(paper|writing)"
  patterns: "plain.?text (paper|academic)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "web_extract"
    required: false
  }
  binaries: "grep"
  binaries: "wc"
}

provides {
  capabilities: "academic_paper_writing"
  capabilities: "style_enforcement"
  capabilities: "reference_verification"
  capabilities: "compliance_checking"
  output_types: ".txt"
  output_types: ".md"
}

actions {
  id: "enforce_style_rules"
  description: "Extract and enforce style constraints from user request"
  trigger_phrases: "apply style rules"
  trigger_phrases: "enforce banned words"
  trigger_phrases: "check style compliance"
    rules {
      text: "Build explicit checklist from user-supplied rules: voice, banned words, banned punctuation, structure, citation format"
      priority: CRITICAL
    }
    rules {
      text: "Banned words must be checked across ALL text including section headings — headings often hide AI tells"
      priority: CRITICAL
    }
    rules {
      text: "Check for em dashes (Unicode — and ASCII --) if banned punctuation specified"
      priority: CRITICAL
    }
    rules {
      text: "Common AI tells to flag: delve, landscape, tapestry, crucial, furthermore, moreover, in conclusion, aforementioned, notably, particularly"
      priority: HIGH
    }
    rules {
      text: "When 'we' voice required: use 'we' for all actions, avoid passive voice, avoid 'the author(s)' and 'this paper'"
      priority: HIGH
    }
    rules {
      text: "Provide replacement suggestions for each banned word"
      priority: NORMAL
    }
    data {
      key: "common_banned_words"
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
          string_value: "in conclusion"
        }
        items {
          string_value: "it is important to note"
        }
        items {
          string_value: "in the realm of"
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
    data {
      key: "check_command"
      string_value: "grep -in 'word1\\|word2\\|word3' paper.txt"
    }
    examples {
      label: "grep for banned words in full file"
      language: "bash"
      code: "grep -in 'delve\\\\|landscape\\\\|tapestry\\\\|crucial' paper.txt\ngrep -c '—' paper.txt  # em dash count, should be 0"
    }
}
actions {
  id: "verify_references"
  description: "Batch-verify arXiv IDs, author lists, and publication years"
  trigger_phrases: "verify references"
  trigger_phrases: "check citations"
  trigger_phrases: "validate arxiv ids"
    rules {
      text: "Never trust arXiv IDs from memory — always fetch the page and verify title matches ID"
      priority: CRITICAL
    }
    rules {
      text: "Batch-verify in groups of 5 URLs max per web_extract call"
      priority: CRITICAL
    }
    rules {
      text: "Check: title matches arXiv ID, author list is correct, year matches submission history"
      priority: HIGH
    }
    rules {
      text: "When arXiv page doesn't render authors: search '\"paper title\" arxiv authors', check Semantic Scholar API, or try HTML version"
      priority: HIGH
    }
    rules {
      text: "Common error: paper title correct but arXiv ID wrong — always cross-check"
      priority: NORMAL
    }
    data {
      key: "arxiv_url_pattern"
      string_value: "https://arxiv.org/abs/{id}"
    }
    data {
      key: "semantic_scholar_pattern"
      string_value: "https://api.semanticscholar.org/graph/v1/paper/arXiv:{id}"
    }
    data {
      key: "html_fallback_pattern"
      string_value: "https://arxiv.org/html/{id}v{version}"
    }
}
actions {
  id: "structure_paper"
  description: "Outline and draft paper following IEEE-style template"
  trigger_phrases: "outline paper"
  trigger_phrases: "structure paper"
  trigger_phrases: "IEEE format"
  trigger_phrases: "draft paper structure"
    rules {
      text: "Standard IEEE sections: Title, Author, Abstract, Index Terms, Introduction, Background/Related Work, Main Sections, Limitations, Conclusion, References"
      priority: HIGH
    }
    rules {
      text: "Abstract: single paragraph, 150-250 words, states problem/approach/results"
      priority: HIGH
    }
    rules {
      text: "Confirm word count target with user: Short (3-4K), Standard (4-6K), Long (6-8K)"
      priority: HIGH
    }
    rules {
      text: "Introduction: problem, motivation, contributions as bullet list, paper organization"
      priority: NORMAL
    }
    data {
      key: "word_count_targets"
      map_value {
        entries {
          key: "short"
          string_value: "3000-4000"
        }
        entries {
          key: "standard"
          string_value: "4000-6000"
        }
        entries {
          key: "long"
          string_value: "6000-8000"
        }
      }
    }
}
actions {
  id: "pre_delivery_check"
  description: "Run all compliance checks before delivering the paper"
  trigger_phrases: "run compliance check"
  trigger_phrases: "pre-delivery check"
  trigger_phrases: "validate paper"
    rules {
      text: "Run ALL checks: word count, em dashes, banned words (entire file incl headings), reference count, section headers, voice consistency"
      priority: CRITICAL
    }
    rules {
      text: "Deliver verification summary with results: word count, em dash count, AI tells found, references verified, sections present, voice check"
      priority: HIGH
    }
    rules {
      text: "Use grep -in for case-insensitive banned word search across whole file"
      priority: NORMAL
    }
    examples {
      label: "full compliance scan"
      language: "bash"
      code: "wc -w paper.txt\ngrep -c '—' paper.txt\ngrep -in 'delve\\|landscape\\|tapestry\\|crucial' paper.txt\ngrep -c '^\\[' paper.txt\ngrep -E '^(I\\.|II\\.|III\\.)' paper.txt\ngrep -c ' we ' paper.txt"
    }
}

guardrails {
  text: "Always grep the entire file for banned words — headings contain AI tells too"
  scope: ALWAYS
}

guardrails {
  text: "Never trust citation data from memory — always fetch and verify programmatically"
  scope: ALWAYS
}

guardrails {
  text: "Use plain text formats (.txt, .md) unless user requests LaTeX"
  scope: ALWAYS
}

related {
  name: "research-paper-writing"
  relationship: "alternative_to"
  description: "LaTeX papers for ML conferences with programmatic citations"
}

related {
  name: "arxiv"
  relationship: "composes_with"
  description: "Search arXiv for papers to cite"
}

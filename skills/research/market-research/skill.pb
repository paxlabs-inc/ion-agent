meta {
  name: "market-research"
  version: "1.0.0"
  summary: "Compile structured market/competitive intelligence from web sources into actionable reference documents"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "market research"
  keywords: "competitive intelligence"
  keywords: "vendor comparison"
  keywords: "landscape scan"
  keywords: "pricing research"
  keywords: "industry mapping"
  intents: "research_market"
  intents: "compare_vendors"
  intents: "compile_intelligence"
  intents: "map_industry"
  patterns: "(research|analyze|map) .*(market|industry|landscape|competitive)"
  patterns: "(find|compare|list) .*(vendors|services|providers|options)"
  patterns: "(pricing|cost|fee) .*(research|comparison|analysis)"
  patterns: "what are the options for"
  patterns: "competitive intelligence"
}

requires {
  tools {
    name: "web_search"
    required: true
  }
  tools {
    name: "web_extract"
    required: false
  }
  tools {
    name: "write_file"
    required: true
  }
}

provides {
  capabilities: "market_intelligence"
  capabilities: "vendor_comparison"
  capabilities: "pricing_research"
  capabilities: "landscape_analysis"
  output_types: ".md"
}

actions {
  id: "scope_research"
  description: "Identify key dimensions and filters for the research"
  trigger_phrases: "scope research"
  trigger_phrases: "define research dimensions"
  trigger_phrases: "plan market research"
    rules {
      text: "Identify key dimensions: pricing, features, coverage, payment terms, integrations, etc."
      priority: HIGH
    }
    rules {
      text: "Note filters: geography, tier, company size, industry vertical"
      priority: HIGH
    }
    rules {
      text: "Confirm scope with user before executing multi-query search"
      priority: NORMAL
    }
}
actions {
  id: "multi_query_search"
  description: "Execute parallel searches varying the angle for breadth"
  trigger_phrases: "search market"
  trigger_phrases: "gather market data"
  trigger_phrases: "find vendors"
    rules {
      text: "Execute 3-6 independent searches in parallel, varying the angle"
      priority: CRITICAL
    }
    rules {
      text: "Search angles: direct name, specific feature, comparison/ranking, cost/fee breakdown"
      priority: HIGH
    }
    rules {
      text: "Read snippets carefully for cross-references and specific data points"
      priority: HIGH
    }
    rules {
      text: "Use web_extract on promising URLs for full-page content"
      priority: HIGH
    }
    rules {
      text: "Don't retry web_extract more than twice on same domain — likely rate-limited; use alternative queries"
      priority: NORMAL
    }
    data {
      key: "search_angles"
      list_value {
        items {
          string_value: "Direct name searches"
        }
        items {
          string_value: "Specific feature searches"
        }
        items {
          string_value: "Comparison/ranking searches"
        }
        items {
          string_value: "Cost/fee breakdown searches"
        }
      }
    }
}
actions {
  id: "cross_reference_validate"
  description: "Validate data points across multiple independent sources"
  trigger_phrases: "validate findings"
  trigger_phrases: "cross-reference data"
    rules {
      text: "Require same data point in 2+ independent sources before treating as confirmed"
      priority: HIGH
    }
    rules {
      text: "Flag single-source claims as 'reported' or 'estimated'"
      priority: HIGH
    }
    rules {
      text: "Note when information is clearly outdated — check dates in snippets"
      priority: NORMAL
    }
}
actions {
  id: "compile_deliverable"
  description: "Structure findings into a reference document"
  trigger_phrases: "compile report"
  trigger_phrases: "create market report"
  trigger_phrases: "write up findings"
    rules {
      text: "Structure: summary table(s) for quick scanning, tiered/grouped organization, explicit confidence markers"
      priority: HIGH
    }
    rules {
      text: "Include actionable takeaway section at the end"
      priority: HIGH
    }
    rules {
      text: "Save to /tmp/ with descriptive filename"
      priority: HIGH
    }
    rules {
      text: "User wants a document they can reference/share, not just a chat answer"
      priority: NORMAL
    }
    rules {
      text: "Be honest about gaps — don't fill unknowns with plausible guesses"
      priority: NORMAL
    }
}

guardrails {
  text: "Never fabricate pricing or contact details you didn't find"
  scope: ALWAYS
}

guardrails {
  text: "Don't stop at first search — surface breadth matters for market research"
  scope: ALWAYS
}

guardrails {
  text: "Negative findings ('X does NOT charge fees') are often the most useful data points — include them"
  scope: ALWAYS
}

guardrails {
  text: "When web_extract fails, compile from snippets — the research doesn't stop"
  scope: ALWAYS
}

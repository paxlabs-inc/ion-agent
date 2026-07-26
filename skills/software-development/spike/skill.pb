meta {
  name: "spike"
  version: "1.0.0"
  summary: "Throwaway experiments to validate an idea before build — decompose, research, build, verdict."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "spike"
  keywords: "prototype"
  keywords: "experiment"
  keywords: "feasibility"
  keywords: "proof of concept"
  keywords: "try this"
  keywords: "quick prototype"
  intents: "run_spike"
  intents: "validate_idea"
  intents: "compare_approaches"
  intents: "feasibility_check"
  patterns: "(spike|prototype|experiment) .*(this|that|idea)"
  patterns: "(try|see if) .*(works|possible)"
  patterns: "before .*(commit|build|implement)"
  patterns: "compare .*(approaches|libraries|tools)"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "terminal"
    required: true
  }
}

provides {
  capabilities: "spike_prototyping"
  capabilities: "feasibility_testing"
  output_types: ".md"
  output_types: ".py"
  output_types: ".js"
  output_types: ".ts"
}

actions {
  id: "run_spike"
  description: "Execute a spike experiment to validate feasibility"
  trigger_phrases: "spike this out"
  trigger_phrases: "try this approach"
  trigger_phrases: "is this even possible"
  trigger_phrases: "quick prototype"
    rules {
      text: "Spikes are disposable by design — throw them away once they've paid their debt"
      priority: CRITICAL
    }
    rules {
      text: "Do NOT use spike for production path work — use the plan skill instead"
      priority: CRITICAL
    }
    rules {
      text: "Decompose into 2-5 independent feasibility questions — each question is one spike"
      priority: HIGH
    }
    rules {
      text: "Order by risk — the spike most likely to kill the idea runs first"
      priority: HIGH
    }
    rules {
      text: "Bias toward something the user can interact with — CLI, HTML page, web server, or test with recognizable assertions"
      priority: HIGH
    }
    rules {
      text: "Never declare 'it works' after one happy-path run — test edge cases, follow surprising findings"
      priority: HIGH
    }
    rules {
      text: "Each spike gets its own directory: spikes/NNN-descriptive-name/"
      priority: NORMAL
    }
    rules {
      text: "Verdict: VALIDATED | PARTIAL | INVALIDATED — with what worked, what didn't, surprises, recommendation"
      priority: NORMAL
    }
    data {
      key: "spike_types"
      list_value {
        items {
          string_value: "standard — one approach answering one question"
        }
        items {
          string_value: "comparison — same question, different approaches (shared number, letter suffix)"
        }
      }
    }
    data {
      key: "output_preference"
      list_value {
        items {
          string_value: "1. Runnable CLI with observable output"
        }
        items {
          string_value: "2. Minimal HTML page demonstrating behavior"
        }
        items {
          string_value: "3. Small web server with one endpoint"
        }
        items {
          string_value: "4. Unit test with recognizable assertions"
        }
      }
    }
    data {
      key: "method_loop"
      string_value: "decompose → research → build → verdict (iterate on findings)"
    }
    examples {
      label: "decompose a feature idea"
      language: "markdown"
      code: "| # | Spike | Validates | Risk |\n|---|-------|-----------|------|\n| 001 | websocket-streaming | WS connection streams tokens <100ms | High |\n| 002a | pdf-parse-pdfjs | pdfjs extracts structured text | Medium |\n| 002b | pdf-parse-camelot | camelot extracts structured text | Medium |"
    }
}
actions {
  id: "compare_approaches"
  description: "Build comparison spikes for head-to-head evaluation"
  trigger_phrases: "compare approaches"
  trigger_phrases: "which library is better"
  trigger_phrases: "a vs b"
    rules {
      text: "Build comparison spikes back to back, then do head-to-head with dimension table"
      priority: HIGH
    }
    rules {
      text: "Dimensions: extraction quality, setup complexity, performance, edge case handling"
      priority: NORMAL
    }
    examples {
      label: "head-to-head comparison"
      language: "markdown"
      code: "## Head-to-head: pdfjs vs camelot\n| Dimension | pdfjs (002a) | camelot (002b) |\n|-----------|--------------|----------------|\n| Extraction quality | 9/10 structured | 7/10 table-only |\n| Setup complexity | npm install, 1 line | pip + ghostscript |\n| Perf on 100-page PDF | 3s | 18s |\n**Winner:** pdfjs for our use case"
    }
}
actions {
  id: "frontier_spike"
  description: "Determine what to spike next from existing spikes"
  trigger_phrases: "what should I spike next"
  trigger_phrases: "next spike"
  trigger_phrases: "spike frontier"
    rules {
      text: "Look for integration risks, data handoffs, gaps in vision, and alternative approaches for PARTIAL/INVALIDATED spikes"
      priority: HIGH
    }
    rules {
      text: "Propose 2-4 candidates as Given/When/Then — let user pick"
      priority: NORMAL
    }
}

guardrails {
  text: "Spikes are disposable — never clean up for production"
  scope: ALWAYS
}

guardrails {
  text: "Don't use spike when the answer is knowable from docs or reading code"
  scope: ALWAYS
}

meta {
  name: "technical-architecture-proposal"
  version: "1.0.0"
  summary: "Produce architecture proposals for adding capabilities to existing codebases — systematic exploration, seam-finding, grounded recommendations."
  author: "Neo"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "architecture proposal"
  keywords: "design doc"
  keywords: "integration plan"
  keywords: "add capability"
  keywords: "extend codebase"
  intents: "create_architecture_proposal"
  intents: "design_integration"
  intents: "plan_extension"
  patterns: "(architecture|design) .*(proposal|doc|plan)"
  patterns: "(how|plan) .*(add|extend|integrate) .*(to|into) .*(system|codebase|project)"
  patterns: "technical plan .*(feature|capability)"
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
  capabilities: "architecture_proposal"
  capabilities: "integration_planning"
  output_types: ".md"
}

actions {
  id: "create_proposal"
  description: "Produce a grounded architecture proposal for a new capability"
  trigger_phrases: "architecture proposal"
  trigger_phrases: "design doc"
  trigger_phrases: "how would we add X"
  trigger_phrases: "technical plan"
    rules {
      text: "Every recommendation must reference a real interface, file, or pattern discovered in the codebase — no generic hand-waving"
      priority: CRITICAL
    }
    rules {
      text: "Explore the codebase BEFORE writing anything — map directory, read build manifest, find core interfaces, trace request flow, find seams"
      priority: CRITICAL
    }
    rules {
      text: "Reference real files and interfaces — 'The Reporter interface in internal/agent/reporter.go defines Say()' is good; 'The agent has an output interface' is bad"
      priority: HIGH
    }
    rules {
      text: "Include concrete code — Go interfaces, type definitions, function signatures, short implementation sketches"
      priority: HIGH
    }
    rules {
      text: "Use comparison tables for provider/service choices — columns: name, key metric, SDK availability, recommendation"
      priority: HIGH
    }
    rules {
      text: "Phase 1 should be simplest useful slice — don't over-architect early phases"
      priority: HIGH
    }
    rules {
      text: "No marketing language — banned: best-in-class, cutting-edge, revolutionary. Say: lowest latency, cheapest, best quality with data"
      priority: NORMAL
    }
    rules {
      text: "No em dashes — use commas, semicolons, or restructure the sentence"
      priority: NORMAL
    }
    data {
      key: "proposal_structure"
      list_value {
        items {
          string_value: "1. Overview — goal, use cases, current state with file references"
        }
        items {
          string_value: "2. Primary Component — provider options, recommendation, interface definition, implementation sketch"
        }
        items {
          string_value: "3. Secondary Component — same structure"
        }
        items {
          string_value: "4. Integration — file/component integration points, new packages, API endpoints, data flow diagram"
        }
        items {
          string_value: "5. Technical Stack — dependencies, config schema, error handling"
        }
        items {
          string_value: "6. Implementation Roadmap — phased delivery, each phase independently deployable"
        }
        items {
          string_value: "7. Security & Privacy — data handling, consent"
        }
        items {
          string_value: "8. Cost Analysis — per-provider cost tables, monthly estimates"
        }
      }
    }
    data {
      key: "exploration_steps"
      list_value {
        items {
          string_value: "Map directory structure"
        }
        items {
          string_value: "Read build manifest (go.mod, package.json, etc.)"
        }
        items {
          string_value: "Find 3-5 load-bearing interfaces/types"
        }
        items {
          string_value: "Trace request/response flow"
        }
        items {
          string_value: "Find seams (plugin interfaces, events, middleware, config injection)"
        }
        items {
          string_value: "Search for existing related code"
        }
      }
    }
    examples {
      label: "good vs bad references"
      language: "text"
      code: "GOOD: \"The Reporter interface in internal/agent/reporter.go defines Say(), Status(), Delta()\"\nBAD: \"The agent has an output interface\"\nGOOD: \"Config via internal/config/config.go: type Config struct { ... }\"\nBAD: \"Add a configuration option\""
    }
}
actions {
  id: "review_proposal"
  description: "Review or refine an existing architecture proposal"
  trigger_phrases: "review architecture proposal"
  trigger_phrases: "improve design doc"
  trigger_phrases: "critique proposal"
    rules {
      text: "Verify every claim references a real file or interface — flag generic statements"
      priority: HIGH
    }
    rules {
      text: "Check that Phase 1 is the simplest useful slice — no over-architecting"
      priority: HIGH
    }
    rules {
      text: "Ensure error handling and fallback chains are documented"
      priority: NORMAL
    }
}

guardrails {
  text: "Never propose without reading the code first — explore before writing"
  scope: ALWAYS
}

guardrails {
  text: "Every recommendation must reference real code — no generic hand-waving"
  scope: ALWAYS
}

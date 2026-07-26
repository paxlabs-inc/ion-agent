meta {
  name: "architecture-pipeline"
  version: "1.0.0"
  summary: "Multi-phase architecture pipeline: research, genesis writing, adversarial security review, revision, and implementation planning."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "architecture pipeline"
  keywords: "security review"
  keywords: "adversarial review"
  keywords: "architecture document"
  keywords: "implementation plan"
  intents: "design_architecture"
  intents: "security_review"
  intents: "implementation_plan"
  intents: "harden_design"
  patterns: "(design|build|create) .*(architecture|system design)"
  patterns: "(security|adversarial) review .*(design|architecture)"
  patterns: "implementation plan .*(architecture|design)"
  patterns: "harden .*(design|architecture)"
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
  capabilities: "architecture_pipeline"
  capabilities: "security_review"
  capabilities: "implementation_planning"
  output_types: ".md"
}

actions {
  id: "architecture_pipeline"
  description: "Execute the 5-phase architecture pipeline with specialized subagents"
  trigger_phrases: "run architecture pipeline"
  trigger_phrases: "design system architecture"
  trigger_phrases: "produce implementation plan"
    rules {
      text: "Do NOT skip the security review — every architecture has blind spots that the adversarial subagent finds"
      priority: CRITICAL
    }
    rules {
      text: "Do NOT produce the implementation plan before the security revision — revision changes data structures, constraints, and adds gates"
      priority: CRITICAL
    }
    rules {
      text: "Do NOT use the same subagent role for security and planning — they require fundamentally different mindsets (adversarial vs constructive)"
      priority: CRITICAL
    }
    rules {
      text: "Phase 2.5 subagent must produce: threat model, attack vectors, defense architecture, safety classification (GREEN/YELLOW/RED), cryptographic spec, day zero/100 checklists"
      priority: HIGH
    }
    rules {
      text: "Phase 3 subagent must produce: language decisions, dependency map, concurrency model, storage architecture, performance budget, build order, testing strategy"
      priority: HIGH
    }
    rules {
      text: "After security review returns, produce a revision document listing every change from original architecture"
      priority: HIGH
    }
    rules {
      text: "Maintain a running corpus summary with document name, word count, and role in pipeline"
      priority: NORMAL
    }
    rules {
      text: "Word count targets: architecture ~5000, security spec 8000-12000, implementation plan 10000-15000"
      priority: NORMAL
    }
    data {
      key: "phases"
      list_value {
        items {
          string_value: "Phase 1: Research Synthesis"
        }
        items {
          string_value: "Phase 2: Genesis Writing"
        }
        items {
          string_value: "Phase 2.5: Adversarial Security Review"
        }
        items {
          string_value: "Phase 3: Implementation Planning"
        }
      }
    }
    data {
      key: "security_review_sections"
      list_value {
        items {
          string_value: "Threat model (adversary classes, attack surfaces, crown jewels)"
        }
        items {
          string_value: "Attack vectors (full enumeration per component)"
        }
        items {
          string_value: "Defense architecture (preventive, detective, recovery, residual risk)"
        }
        items {
          string_value: "Safety classification (GREEN/YELLOW/RED per capability)"
        }
        items {
          string_value: "Cryptographic specification (algorithms, key hierarchy, rotation)"
        }
        items {
          string_value: "Day Zero checklist (testable requirements before code)"
        }
        items {
          string_value: "Day 100 checklist (requirements before autonomous features)"
        }
        items {
          string_value: "Residual risk register (accept/transfer/mitigate)"
        }
      }
    }
    examples {
      label: "security reviewer subagent prompt"
      language: "text"
      code: "Role: Security architect + adversarial red-teamer\nGoal: Attack the architecture from every angle\nDemand: BRUTAL, assume the worst, assume every component will be targeted"
    }
}
actions {
  id: "inspect_pipeline"
  description: "Review or inspect an existing architecture pipeline document set"
  trigger_phrases: "review architecture"
  trigger_phrases: "check pipeline"
  trigger_phrases: "inspect architecture docs"
    rules {
      text: "Read ALL prior documents before making recommendations"
      priority: HIGH
    }
    rules {
      text: "Check that revision document integrates all security findings back into architecture"
      priority: NORMAL
    }
}

guardrails {
  text: "Never skip the adversarial security review phase"
  scope: ALWAYS
}

guardrails {
  text: "Never let the security reviewer be polite — demand BRUTAL analysis"
  scope: ALWAYS
}

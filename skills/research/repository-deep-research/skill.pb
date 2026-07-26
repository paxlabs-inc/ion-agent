meta {
  name: "repository-deep-research"
  version: "1.0.0"
  summary: "Deep research of GitHub repos — clone and read source, not just READMEs; dual non-technical + technical reports"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "deep research"
  keywords: "repo breakdown"
  keywords: "source analysis"
  keywords: "codebase analysis"
  keywords: "technical dive"
  keywords: "architecture truth"
  intents: "deep_repo_analysis"
  intents: "technical_breakdown"
  intents: "codebase_audit"
  intents: "theory_grounded_analysis"
  patterns: "(deep research|full breakdown|technical dive) .*(repo|repository|github)"
  patterns: "(read the code|not just docs|dont be lazy)"
  patterns: "(analyze|examine|audit) .*(codebase|monorepo|repo)"
  patterns: "github\\.com/.+/.+ .*(deep|analysis|breakdown)"
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
    name: "read_file"
    required: true
  }
  tools {
    name: "web_search"
    required: false
  }
  binaries: "git"
  binaries: "curl"
  binaries: "python3"
}

provides {
  capabilities: "repo_deep_dive"
  capabilities: "source_analysis"
  capabilities: "architecture_mapping"
  capabilities: "dual_report"
  capabilities: "theory_grounded_analysis"
  output_types: ".md"
}

actions {
  id: "orient"
  description: "Cheap pass — GitHub API metadata + README for orientation only"
  trigger_phrases: "orient to repo"
  trigger_phrases: "get repo overview"
  trigger_phrases: "repo metadata"
    rules {
      text: "README/ARCHITECTURE.md is orientation only — do not stop here for deep dives"
      priority: CRITICAL
    }
    rules {
      text: "GitHub API: description, stars, dates, languages, license, topics, contributors, recent commits"
      priority: HIGH
    }
    rules {
      text: "Tree inventory: top-level dirs, module roots, file counts"
      priority: HIGH
    }
    rules {
      text: "curl pipe to python3 blocked by security scanners in some envs — use two-step: curl -o /tmp/file.json && python3 -c '...' < /tmp/file.json"
      priority: NORMAL
    }
    data {
      key: "github_api_url"
      string_value: "https://api.github.com/repos/{owner}/{repo}"
    }
}
actions {
  id: "clone_and_map"
  description: "Clone the repo and inventory modules"
  trigger_phrases: "clone repo"
  trigger_phrases: "map codebase"
  trigger_phrases: "inventory modules"
    rules {
      text: "git clone --depth 1 for efficiency unless full history needed"
      priority: HIGH
    }
    rules {
      text: "Inventory modules, Makefile install targets, frozen specs, skills/docs trees"
      priority: HIGH
    }
    rules {
      text: "For large repos (50+ modules): prioritize by wc -l on candidate files to find heaviest, read entry points first"
      priority: NORMAL
    }
}
actions {
  id: "source_first_deep_dive"
  description: "Read source code for load-bearing subsystems"
  trigger_phrases: "read source"
  trigger_phrases: "deep dive source"
  trigger_phrases: "source-first analysis"
    rules {
      text: "For deep dives or named subsystems, clone the repo and read source: entrypoints, control loops, types, store paths, failure modes"
      priority: CRITICAL
    }
    rules {
      text: "If user names specific modules (Neo/Cortex/etc), those source trees are mandatory — read them"
      priority: CRITICAL
    }
    rules {
      text: "Read: cmd/main entry (boot wiring), core package facade/loop (control flow), store/journal (persistence invariants), failure/incomplete types (honesty model), tests (contract enforcement)"
      priority: HIGH
    }
    rules {
      text: "Language-adaptive symbol search: Go→func/WriteBatch, Python→class/def, TypeScript→class/interface, Rust→impl/fn/trait"
      priority: HIGH
    }
    rules {
      text: "Prefer implementation comments and tests over polished docs when they disagree"
      priority: HIGH
    }
    rules {
      text: "For 3K+ line files: read first 100-200 lines, then search for class/function definitions, read specific sections"
      priority: NORMAL
    }
    data {
      key: "language_symbols"
      map_value {
        entries {
          key: "go"
          string_value: "func (a *Agent) Chat, WriteBatch, Verdict, type ... interface"
        }
        entries {
          key: "python"
          string_value: "class AIAgent, def run_conversation, registry.register(), @dataclass"
        }
        entries {
          key: "typescript"
          string_value: "class Agent {, async run(, interface Tool, export function"
        }
        entries {
          key: "rust"
          string_value: "impl Agent, fn run(, trait Tool, pub fn dispatch"
        }
      }
    }
}
actions {
  id: "cross_check_docs_vs_code"
  description: "Call out divergences between docs and actual implementation"
  trigger_phrases: "cross-check docs"
  trigger_phrases: "docs vs code"
  trigger_phrases: "verify claims"
    rules {
      text: "Call out divergences: design says X but implementation does Y; frozen spec still binds but feature retired; etc."
      priority: HIGH
    }
    rules {
      text: "Frozen design (.kvx / design docs) can be aspirational or superseded — verify enablement flags and live call sites"
      priority: NORMAL
    }
}
actions {
  id: "deliver_dual_report"
  description: "Produce both non-technical and technical reports"
  trigger_phrases: "write report"
  trigger_phrases: "deliver findings"
  trigger_phrases: "dual report"
    rules {
      text: "Non-technical: product story, layers, capabilities, who-for, status"
      priority: HIGH
    }
    rules {
      text: "Technical: facts, architecture, module maturity, deep dives, security, strengths, risks, how to evaluate"
      priority: HIGH
    }
    rules {
      text: "Dual deliverable is default when user asks for full breakdown + report"
      priority: HIGH
    }
    rules {
      text: "Never fabricate tool output — if clone/API fails, say so; never invent stars, LOC, or file contents"
      priority: NORMAL
    }
}
actions {
  id: "theory_grounded_analysis"
  description: "Analyze codebase against academic papers or specs"
  trigger_phrases: "analyze against paper"
  trigger_phrases: "theory-grounded analysis"
  trigger_phrases: "code vs paper"
    rules {
      text: "Read the theory FIRST completely — extract full text including omitted middles. Do not reason about codebase until framework is fully loaded"
      priority: CRITICAL
    }
    rules {
      text: "Build correspondence table: theory concept → code artifact. This is the primary deliverable"
      priority: CRITICAL
    }
    rules {
      text: "Look for self-referential patterns — systems that embody their own theory are highest-signal findings"
      priority: HIGH
    }
    rules {
      text: "Evaluate, don't just map — call out where code validates, extends, or contradicts the theory"
      priority: HIGH
    }
    rules {
      text: "Name the tensions between contradictory theoretical claims — show where code resolves or amplifies them"
      priority: HIGH
    }
    rules {
      text: "Named modules must be read as source, not docs — read agent.go, premise.go, prediction.go etc."
      priority: NORMAL
    }
}

guardrails {
  text: "Reading READMEs and ARCHITECTURE.md is orientation only — source-first is mandatory for deep dives"
  scope: ALWAYS
}

guardrails {
  text: "Never claim dependency/integration relationships without verifying in source (go.mod, package.json, imports)"
  scope: ALWAYS
}

guardrails {
  text: "Never fabricate tool output — if clone/API fails, say so"
  scope: ALWAYS
}

guardrails {
  text: "Don't conflate correction with explanation — fix factual errors silently and move on"
  scope: ALWAYS
}

guardrails {
  text: "Pass file paths not content to synthesis agents — inline report content truncates and bloats context"
  scope: ALWAYS
}

related {
  name: "oss-developer-traction"
  relationship: "alternative_to"
  description: "Pre-launch GitHub/dev GTM and star-worthiness (packaging/distribution, not source-first)"
}

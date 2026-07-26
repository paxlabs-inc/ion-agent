meta {
  name: "oss-developer-traction"
  version: "1.0.0"
  summary: "Research and plan GitHub + developer-community traction for OSS or agent products — baseline metrics, competitor gravity, pre-launch GTM"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "github stars"
  keywords: "developer traction"
  keywords: "oss growth"
  keywords: "pre-launch gtm"
  keywords: "developer mindshare"
  keywords: "star-worthiness"
  keywords: "launch playbook"
  intents: "analyze_traction"
  intents: "plan_gtm"
  intents: "audit_repo"
  intents: "compare_competitors"
  patterns: "(more|get|increase) .*(github stars|forks|dev mindshare)"
  patterns: "(oss|open.?source) .*(growth|traction|launch|gtm)"
  patterns: "(compare|benchmark) .*(agent|framework|tool) .*(gravity|traction)"
  patterns: "(audit|evaluate) .*(repo|repository) .*(star|worthy)"
  patterns: "pre.?launch .*(plan|strategy|playbook)"
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
    name: "web_search"
    required: false
  }
  binaries: "curl"
  binaries: "python3"
}

provides {
  capabilities: "traction_analysis"
  capabilities: "competitor_benchmarking"
  capabilities: "gtm_planning"
  capabilities: "repo_audit"
  output_types: ".md"
}

actions {
  id: "baseline_product_surface"
  description: "Collect live GitHub metrics for the hero repo and org"
  trigger_phrases: "baseline metrics"
  trigger_phrases: "github stats"
  trigger_phrases: "repo health check"
    rules {
      text: "Measure live from APIs, never guess — stars, forks, issues, license, discussions, HN presence"
      priority: CRITICAL
    }
    rules {
      text: "Use GitHub API: GET /repos/{owner}/{repo} for core stats, /community/profile for health, /issues (drop PRs)"
      priority: CRITICAL
    }
    rules {
      text: "Check: owner type (User vs Org), license optics (README badge + LICENSE file), HN footprint via Algolia API"
      priority: HIGH
    }
    rules {
      text: "Probe docs/marketing sites for HTTP status and hero messaging"
      priority: HIGH
    }
    rules {
      text: "Write a small Python probe script to a file and run it — nested python3 -c with heavy quoting fails often"
      priority: NORMAL
    }
    data {
      key: "github_api_endpoints"
      map_value {
        entries {
          key: "repo"
          string_value: "GET /repos/{owner}/{repo}"
        }
        entries {
          key: "languages"
          string_value: "GET /repos/{owner}/{repo}/languages"
        }
        entries {
          key: "community"
          string_value: "GET /repos/{owner}/{repo}/community/profile"
        }
        entries {
          key: "issues"
          string_value: "GET /repos/{owner}/{repo}/issues"
        }
        entries {
          key: "releases"
          string_value: "GET /repos/{owner}/{repo}/releases"
        }
        entries {
          key: "owner"
          string_value: "GET /users/{login}"
        }
      }
    }
    data {
      key: "hn_search_url"
      string_value: "https://hn.algolia.com/api/v1/search?query={query}&tags=story"
    }
}
actions {
  id: "category_gravity"
  description: "Sample 4-8 peers in same job-to-be-done for distribution lessons"
  trigger_phrases: "competitor analysis"
  trigger_phrases: "category comparison"
  trigger_phrases: "peer benchmarking"
    rules {
      text: "Sample 4-8 peers in same job-to-be-done, not every star chart"
      priority: HIGH
    }
    rules {
      text: "For each peer: approximate stars, wedge one-liner, what they did for distribution (Show HN, PH, awesome-lists, one-command demo)"
      priority: HIGH
    }
    rules {
      text: "Pattern: devs star what they can run in one sitting and explain in one tweet"
      priority: NORMAL
    }
}
actions {
  id: "diagnose_blockers"
  description: "Rank what is blocking GitHub traction for this repo"
  trigger_phrases: "diagnose blockers"
  trigger_phrases: "why low stars"
  trigger_phrases: "traction blockers"
    rules {
      text: "Rank blockers: time-to-first-success, closed collaboration (0 issues, Discussions off), license optics, wrong narrative, name collisions, missing thin SDK, no multi-channel launch, diluted hero"
      priority: HIGH
    }
    rules {
      text: "Custom license without plain-English FAQ causes two-second bounce at 'Other'"
      priority: NORMAL
    }
}
actions {
  id: "deliver_recommendations"
  description: "Priority stack of moves + 30/60/90 plan + dual deliverable"
  trigger_phrases: "traction plan"
  trigger_phrases: "gtm recommendations"
  trigger_phrases: "launch plan"
    rules {
      text: "Dual deliverable: chat-facing executive summary AND full markdown report file"
      priority: CRITICAL
    }
    rules {
      text: "Priority stack of ≤7 moves + 30/60/90 plan"
      priority: HIGH
    }
    rules {
      text: "Default high-ROI order: one-command demo → README + demo media → open Discussions + templates → license FAQ → thin client → Show HN → awesome-list PRs"
      priority: HIGH
    }
    rules {
      text: "Label planning star ranges as contingent targets, not forecasts"
      priority: NORMAL
    }
    rules {
      text: "Cite live measurements and web sources"
      priority: NORMAL
    }
}

guardrails {
  text: "Never recommend bought stars or engagement farms — reputation poison on HN and eng Twitter"
  scope: ALWAYS
}

guardrails {
  text: "Stars are vanity unless paired with proof: forks, external issues/PRs, dependents, demos"
  scope: ALWAYS
}

guardrails {
  text: "Measure live from APIs, never from memory"
  scope: ALWAYS
}

related {
  name: "repository-deep-research"
  relationship: "alternative_to"
  description: "Clone + source for architecture truth (not packaging/distribution)"
}

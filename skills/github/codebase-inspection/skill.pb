meta {
  name: "codebase-inspection"
  version: "1.0.0"
  summary: "Inspect codebases with pygount — LOC, languages, ratios"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "pygount"
  keywords: "LOC"
  keywords: "code analysis"
  keywords: "codebase metrics"
  keywords: "language breakdown"
  keywords: "lines of code"
  intents: "inspect_codebase"
  intents: "count_loc"
  intents: "language_breakdown"
  intents: "code_metrics"
  patterns: "(count|how many) .*(lines|LOC|code)"
  patterns: "(language|codebase) .*(breakdown|composition|size)"
  patterns: "pygount"
  patterns: "how big is (this|the) repo"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "pygount"
}

provides {
  capabilities: "codebase_inspection"
  capabilities: "loc_count"
  capabilities: "language_analysis"
}

actions {
  id: "inspect_codebase"
  description: "Analyze a repository for line counts, language distribution, and code-to-comment ratios"
  trigger_phrases: "inspect codebase"
  trigger_phrases: "count lines of code"
  trigger_phrases: "language breakdown"
  trigger_phrases: "how big is this repo"
    rules {
      text: "Always use --folders-to-skip to exclude .git, node_modules, venv, __pycache__, dist, build — otherwise pygount hangs on large dependency trees"
      priority: CRITICAL
    }
    rules {
      text: "Default to --format=summary for overview; use --format=json for programmatic consumption"
      priority: HIGH
    }
    rules {
      text: "Adjust folder exclusions per project type: Python (.venv, __pycache__), JS (node_modules, .next), Go (vendor)"
      priority: HIGH
    }
    rules {
      text: "Use --suffix to filter by language when only specific types are needed"
      priority: NORMAL
    }
    rules {
      text: "Markdown shows 0 code lines — pygount classifies all Markdown as comments. This is expected."
      priority: NORMAL
    }
    rules {
      text: "For large monorepos, target specific languages with --suffix rather than scanning everything"
      priority: NORMAL
    }
    data {
      key: "default_skip_folders"
      string_value: ".git,node_modules,venv,.venv,__pycache__,.cache,dist,build,.next,.tox,.eggs,*.egg-info"
    }
    data {
      key: "output_formats"
      list_value {
        items {
          string_value: "summary"
        }
        items {
          string_value: "json"
        }
      }
    }
    data {
      key: "pseudo_languages"
      list_value {
        items {
          string_value: "__empty__"
        }
        items {
          string_value: "__binary__"
        }
        items {
          string_value: "__generated__"
        }
        items {
          string_value: "__duplicate__"
        }
        items {
          string_value: "__unknown__"
        }
      }
    }
    examples {
      label: "basic summary scan"
      language: "bash"
      code: "pygount --format=summary \\\n  --folders-to-skip=\".git,node_modules,venv,.venv,__pycache__,.cache,dist,build\" \\\n  ."
    }
    examples {
      label: "filter by language"
      language: "bash"
      code: "pygount --suffix=py,yaml,yml --format=summary ."
    }
    examples {
      label: "JSON output"
      language: "bash"
      code: "pygount --format=json ."
    }
}

guardrails {
  text: "Always pass --folders-to-skip to exclude dependency/build directories"
  scope: ALWAYS
}

guardrails {
  text: "Never run pygount without exclusions on repos with node_modules or venv — it will hang"
  scope: ALWAYS
}

related {
  name: "github-repo-management"
  relationship: "composes_with"
  description: "Repo cloning/creation before inspection"
}

meta {
  name: "requesting-code-review"
  version: "2.0.0"
  summary: "Pre-commit review pipeline — security scan, quality gates, independent reviewer subagent, auto-fix loop."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "code review"
  keywords: "pre-commit review"
  keywords: "verify code"
  keywords: "security scan"
  keywords: "quality gate"
  keywords: "ship code"
  intents: "review_code"
  intents: "verify_before_commit"
  intents: "pre_commit_check"
  patterns: "(review|verify|check) .*(code|commit|push|before merge)"
  patterns: "(commit|push|ship|done) .*(review|verify)"
  patterns: "/review"
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
}

provides {
  capabilities: "code_review"
  capabilities: "security_scan"
  capabilities: "auto_fix"
}

actions {
  id: "pre_commit_review"
  description: "Run the full pre-commit verification pipeline"
  trigger_phrases: "review my code"
  trigger_phrases: "verify before commit"
  trigger_phrases: "pre-commit check"
  trigger_phrases: "review before push"
    rules {
      text: "No agent should verify its own work — use an independent reviewer subagent with no shared context"
      priority: CRITICAL
    }
    rules {
      text: "Security scan: check for hardcoded secrets, shell injection, eval/exec, unsafe deserialization, SQL injection in added lines"
      priority: CRITICAL
    }
    rules {
      text: "Baseline comparison: stash changes, run tests, pop — only NEW failures block the commit"
      priority: HIGH
    }
    rules {
      text: "Auto-fix loop: maximum 2 fix-and-reverify cycles, then escalate to user"
      priority: HIGH
    }
    rules {
      text: "Reviewer subagent returns JSON: {passed, security_concerns, logic_errors, suggestions, summary}"
      priority: HIGH
    }
    rules {
      text: "Commit prefix: [verified] — indicates independent reviewer approved"
      priority: NORMAL
    }
    data {
      key: "security_patterns"
      list_value {
        items {
          string_value: "Hardcoded secrets: (api_key|secret|password|token)\\s*=\\s*['\\\"][^'\\\"]{6,}"
        }
        items {
          string_value: "Shell injection: os.system(, subprocess.*shell=True"
        }
        items {
          string_value: "Dangerous eval/exec: eval(, exec("
        }
        items {
          string_value: "Unsafe deserialization: pickle.loads?("
        }
        items {
          string_value: "SQL injection: execute(f\", .format(.*SELECT"
        }
      }
    }
    data {
      key: "self_review_checklist"
      list_value {
        items {
          string_value: "No hardcoded secrets, API keys, or credentials"
        }
        items {
          string_value: "Input validation on user-provided data"
        }
        items {
          string_value: "SQL queries use parameterized statements"
        }
        items {
          string_value: "File operations validate paths (no traversal)"
        }
        items {
          string_value: "External calls have error handling"
        }
        items {
          string_value: "No debug print/console.log left behind"
        }
        items {
          string_value: "No commented-out code"
        }
        items {
          string_value: "New code has tests"
        }
      }
    }
    examples {
      label: "security scan commands"
      language: "bash"
      code: "# Hardcoded secrets\ngit diff --cached | grep \"^+\" | grep -iE \"(api_key|secret|password|token)\\\\s*=\\\\s*['\\\\\\\"][^'\\\\\\\"]{6,}['\\\\\\\"]\"\n# Shell injection\ngit diff --cached | grep \"^+\" | grep -E \"os\\\\.system\\\\(|subprocess.*shell=True\""
    }
}
actions {
  id: "auto_fix"
  description: "Fix issues found during code review"
  trigger_phrases: "fix review issues"
  trigger_phrases: "auto-fix"
  trigger_phrases: "fix security issues"
    rules {
      text: "Spawn a THIRD agent context — not the implementer, not the reviewer"
      priority: HIGH
    }
    rules {
      text: "Fix ONLY reported issues — do NOT refactor, rename, or add features"
      priority: HIGH
    }
    rules {
      text: "After fix, re-run full verification cycle (Steps 1-6)"
      priority: NORMAL
    }
    examples {
      label: "fix agent delegation"
      language: "text"
      code: "Goal: Fix ONLY the specific issues listed below.\nDo NOT refactor, rename, or change anything else.\nIssues: [INSERT security_concerns AND logic_errors]"
    }
}

guardrails {
  text: "Never let the implementer verify their own work — always use independent reviewer"
  scope: ALWAYS
}

guardrails {
  text: "Maximum 2 auto-fix cycles before escalating to user"
  scope: ALWAYS
}

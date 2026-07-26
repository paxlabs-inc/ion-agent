meta {
  name: "simplify-code"
  version: "1.0.0"
  summary: "Parallel 3-agent review and cleanup of recent code changes — reuse, quality, and efficiency reviewers."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "simplify"
  keywords: "simplify code"
  keywords: "review changes"
  keywords: "clean up code"
  keywords: "simplify changes"
  intents: "simplify_code"
  intents: "review_changes"
  intents: "cleanup_code"
  patterns: "simplify"
  patterns: "(review|clean up) .*(my changes|code|recent changes)"
  patterns: "/simplify"
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
  capabilities: "code_simplification"
  capabilities: "parallel_review"
}

actions {
  id: "simplify_code"
  description: "Run three focused reviewers in parallel and apply fixes"
  trigger_phrases: "simplify"
  trigger_phrases: "simplify my changes"
  trigger_phrases: "simplify these changes"
  trigger_phrases: "/simplify"
    rules {
      text: "Give the WHOLE diff to each reviewer — splitting defeats cross-file duplication and N+1 detection"
      priority: CRITICAL
    }
    rules {
      text: "Reviewers must search the codebase for evidence — a finding with no file:line pointer is noise"
      priority: CRITICAL
    }
    rules {
      text: "Apply Chesterton's Fence: run git blame before flagging anything for removal — if you can't determine purpose, mark confidence: low"
      priority: HIGH
    }
    rules {
      text: "Risk tiers: SAFE (auto-apply), CAREFUL (apply with verification), RISKY (flag for human review)"
      priority: HIGH
    }
    rules {
      text: "Apply SAFE first, then CAREFUL (one file at a time with test verification), then RISKY (never auto-apply)"
      priority: HIGH
    }
    rules {
      text: "Three reviewers: Code Reuse, Code Quality, Efficiency — don't fan out wider than 3"
      priority: NORMAL
    }
    rules {
      text: "Report format: file:line → problem → suggested fix | confidence: high/medium/low | risk: SAFE/CAREFUL/RISKY"
      priority: NORMAL
    }
    rules {
      text: "Respect project conventions — if repo has AGENTS.md/CLAUDE.md/ION.md, fold those rules into reviewer prompts"
      priority: NORMAL
    }
    data {
      key: "reviewer_1_reuse"
      string_value: "Review for code that duplicates functionality already in the codebase — search utility modules, shared helpers, adjacent files for existing functions the new code could call instead"
    }
    data {
      key: "reviewer_2_quality"
      string_value: "Review for redundant state, parameter sprawl, copy-paste-with-variation, leaky abstractions, stringly-typed code, AI-generated slop patterns"
    }
    data {
      key: "reviewer_3_efficiency"
      string_value: "Review for unnecessary work, missed concurrency, hot-path bloat, TOCTOU anti-patterns, memory issues, overly broad reads, silent failures"
    }
    data {
      key: "resolution_order"
      string_value: "correctness > user's stated focus > readability/reuse > micro-perf"
    }
    examples {
      label: "launch three reviewers in parallel"
      language: "text"
      code: "delegate_task(tasks=[\n    {\"goal\": \"Reviewer 1 — Code Reuse: search for duplicate functionality...\", \"toolsets\": [\"terminal\", \"file\", \"search\"]},\n    {\"goal\": \"Reviewer 2 — Code Quality: review for quality problems...\", \"toolsets\": [\"terminal\", \"file\", \"search\"]},\n    {\"goal\": \"Reviewer 3 — Efficiency: review for efficiency problems...\", \"toolsets\": [\"terminal\", \"file\", \"search\"]}\n])"
    }
}
actions {
  id: "dry_run_simplify"
  description: "Run reviewers but don't apply changes — report only"
  trigger_phrases: "simplify dry run"
  trigger_phrases: "just report simplify"
  trigger_phrases: "simplify but don't change"
    rules {
      text: "Present all three risk tiers and apply nothing — ask before applying"
      priority: HIGH
    }
}

guardrails {
  text: "Don't auto-run after every edit — invoke only when user explicitly asks"
  scope: ALWAYS
}

guardrails {
  text: "Don't rewrite beyond what the diff touched — keep edits scoped"
  scope: ALWAYS
}

meta {
  name: "plan"
  version: "2.0.0"
  summary: "Plan mode — write an actionable markdown plan with bite-sized tasks, exact paths, and complete code."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "plan"
  keywords: "planning"
  keywords: "implementation plan"
  keywords: "design plan"
  keywords: "plan mode"
  intents: "create_plan"
  intents: "plan_implementation"
  intents: "plan_feature"
  patterns: "(create|write|make) .*(plan|implementation plan)"
  patterns: "/plan"
  patterns: "plan .*(feature|implementation|design)"
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
  capabilities: "implementation_planning"
  output_types: ".md"
}

actions {
  id: "create_plan"
  description: "Write a concrete, actionable implementation plan"
  trigger_phrases: "create a plan"
  trigger_phrases: "plan this feature"
  trigger_phrases: "write implementation plan"
  trigger_phrases: "/plan"
    rules {
      text: "Do NOT implement code — only write the plan markdown file"
      priority: CRITICAL
    }
    rules {
      text: "Save plan to .ion/plans/YYYY-MM-DD_HHMMSS-<slug>.md"
      priority: CRITICAL
    }
    rules {
      text: "Each task = 2-5 minutes of focused work — one action per step"
      priority: HIGH
    }
    rules {
      text: "Include exact file paths (not 'the config file' but src/config/settings.py)"
      priority: HIGH
    }
    rules {
      text: "Include complete code examples — copy-pasteable, not pseudocode"
      priority: HIGH
    }
    rules {
      text: "Include exact commands with expected output"
      priority: HIGH
    }
    rules {
      text: "Plan structure: Header (Goal, Architecture, Tech Stack) → Tasks → Risks"
      priority: NORMAL
    }
    rules {
      text: "DRY, YAGNI, TDD principles applied"
      priority: NORMAL
    }
    rules {
      text: "Offer execution via subagent-driven-development after saving"
      priority: NORMAL
    }
    data {
      key: "plan_structure"
      list_value {
        items {
          string_value: "Header: Goal, Architecture, Tech Stack"
        }
        items {
          string_value: "Tasks: numbered, bite-sized (2-5 min each)"
        }
        items {
          string_value: "Each task: Objective, Files, Steps with code, Verification"
        }
        items {
          string_value: "Footer: Risks, tradeoffs, open questions"
        }
      }
    }
    data {
      key: "task_format"
      map_value {
        entries {
          key: "objective"
          string_value: "One sentence what this task accomplishes"
        }
        entries {
          key: "files"
          string_value: "Create/Modify/Test with exact paths"
        }
        entries {
          key: "step_pattern"
          string_value: "Write failing test → Run to verify failure → Write minimal code → Run to verify pass → Commit"
        }
      }
    }
    examples {
      label: "correct task granularity"
      language: "markdown"
      code: "### Task 1: Create User model with email field\n**Files:** Create: src/models/user.py\n**Step 1:** Write the model class with email field\n**Step 2:** Run: pytest tests/test_user.py -v\n**Step 3:** git add src/models/user.py && git commit -m \"feat: add User model\""
    }
}
actions {
  id: "review_plan"
  description: "Review or refine an existing implementation plan"
  trigger_phrases: "review plan"
  trigger_phrases: "improve plan"
  trigger_phrases: "refine plan"
    rules {
      text: "Check tasks are sequential, logical, and each is bite-sized"
      priority: HIGH
    }
    rules {
      text: "Verify file paths are exact and code examples are complete"
      priority: HIGH
    }
    rules {
      text: "Ensure DRY, YAGNI, TDD principles are applied"
      priority: NORMAL
    }
}

guardrails {
  text: "Plan mode is read-only — only the plan file may be written"
  scope: ALWAYS
}

guardrails {
  text: "Never implement code during plan mode"
  scope: ALWAYS
}

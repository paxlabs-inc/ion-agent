meta {
  name: "github-pr-workflow"
  version: "1.1.0"
  summary: "GitHub PR lifecycle — branch, commit, open, CI, merge"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "pull request"
  keywords: "PR"
  keywords: "merge"
  keywords: "squash"
  keywords: "rebase"
  keywords: "CI checks"
  keywords: "branch"
  keywords: "commit"
  keywords: "push"
  intents: "create_pr"
  intents: "push_branch"
  intents: "monitor_ci"
  intents: "merge_pr"
  intents: "auto_fix_ci"
  intents: "pr_workflow"
  patterns: "(create|open|submit) .*(PR|pull request)"
  patterns: "(merge|squash) .*(PR|pull request)"
  patterns: "(monitor|check|watch) .*(CI|checks|status)"
  patterns: "(auto-?fix|fix) .*(CI|build|test) .*(fail|error)"
  patterns: "pr (create|merge|diff|list|view)"
}

requires {
  env_any: "GITHUB_TOKEN"
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: false
  }
  binaries: "git"
}

provides {
  capabilities: "pr_creation"
  capabilities: "ci_monitoring"
  capabilities: "pr_merge"
  capabilities: "auto_fix_ci"
}

actions {
  id: "branch_and_commit"
  description: "Create a branch and make commits"
  trigger_phrases: "create a branch"
  trigger_phrases: "make a commit"
  trigger_phrases: "push changes"
    rules {
      text: "Branch naming: feat/, fix/, refactor/, docs/, ci/ prefix + description"
      priority: HIGH
    }
    rules {
      text: "Conventional Commits: type(scope): description. Types: feat, fix, refactor, docs, test, ci, chore, perf"
      priority: HIGH
    }
    rules {
      text: "Always fetch and pull main before branching: git fetch origin && git checkout main && git pull"
      priority: NORMAL
    }
    data {
      key: "branch_prefixes"
      list_value {
        items {
          string_value: "feat/"
        }
        items {
          string_value: "fix/"
        }
        items {
          string_value: "refactor/"
        }
        items {
          string_value: "docs/"
        }
        items {
          string_value: "ci/"
        }
      }
    }
    data {
      key: "commit_types"
      list_value {
        items {
          string_value: "feat"
        }
        items {
          string_value: "fix"
        }
        items {
          string_value: "refactor"
        }
        items {
          string_value: "docs"
        }
        items {
          string_value: "test"
        }
        items {
          string_value: "ci"
        }
        items {
          string_value: "chore"
        }
        items {
          string_value: "perf"
        }
      }
    }
}
actions {
  id: "create_pr"
  description: "Push branch and create a pull request"
  trigger_phrases: "create PR"
  trigger_phrases: "open a pull request"
  trigger_phrases: "submit PR"
    rules {
      text: "Always push before creating PR: git push -u origin HEAD"
      priority: CRITICAL
    }
    rules {
      text: "gh: gh pr create --title '...' --body '...' [--draft] [--reviewer user] [--label '...']"
      priority: HIGH
    }
    rules {
      text: "curl: POST /repos/{o}/{r}/pulls with title, body, head (branch), base (main)"
      priority: HIGH
    }
    rules {
      text: "Include 'Closes #N' in body to auto-link issues"
      priority: NORMAL
    }
    examples {
      label: "create PR with gh"
      language: "bash"
      code: "git push -u origin HEAD\ngh pr create \\\n  --title \"feat: add JWT authentication\" \\\n  --body \"## Summary\\n- Login/register endpoints\\n- JWT token handling\\n\\nCloses #42\" \\\n  --reviewer \"teammate\" \\\n  --label \"enhancement\""
    }
}
actions {
  id: "monitor_ci"
  description: "Check and monitor CI status for a PR"
  trigger_phrases: "check CI"
  trigger_phrases: "CI status"
  trigger_phrases: "are checks passing"
  trigger_phrases: "monitor CI"
    rules {
      text: "gh: gh pr checks [--watch] for polling"
      priority: HIGH
    }
    rules {
      text: "curl: GET /repos/{o}/{r}/commits/{sha}/status and /commits/{sha}/check-runs"
      priority: HIGH
    }
    rules {
      text: "Polling loop: check every 30s, max 10 minutes, break on success/failure/error"
      priority: NORMAL
    }
}
actions {
  id: "auto_fix_ci"
  description: "Diagnose and fix CI failures automatically"
  trigger_phrases: "fix CI"
  trigger_phrases: "auto-fix build"
  trigger_phrases: "CI is failing"
  trigger_phrases: "fix failing tests"
    rules {
      text: "Auto-fix loop: check status → read logs → fix code → commit → push → re-check (max 3 attempts, then ask user)"
      priority: CRITICAL
    }
    rules {
      text: "gh: gh run list --branch <branch>, gh run view <ID> --log-failed"
      priority: HIGH
    }
    rules {
      text: "curl: GET /repos/{o}/{r}/actions/runs?branch=<branch>, download logs zip"
      priority: HIGH
    }
    rules {
      text: "After fixing, always re-verify CI status before declaring success"
      priority: NORMAL
    }
}
actions {
  id: "merge_pr"
  description: "Merge a pull request"
  trigger_phrases: "merge PR"
  trigger_phrases: "squash and merge"
  trigger_phrases: "merge pull request"
    rules {
      text: "gh: gh pr merge --squash --delete-branch (or --auto for auto-merge when green)"
      priority: HIGH
    }
    rules {
      text: "curl: PUT /repos/{o}/{r}/pulls/<N>/merge with merge_method: squash|merge|rebase"
      priority: HIGH
    }
    rules {
      text: "Delete remote branch after merge: git push origin --delete <branch>"
      priority: NORMAL
    }
    data {
      key: "merge_methods"
      list_value {
        items {
          string_value: "merge"
        }
        items {
          string_value: "squash"
        }
        items {
          string_value: "rebase"
        }
      }
    }
}

guardrails {
  text: "Always verify CI is green before merging"
  scope: WRITE_OPS
}

guardrails {
  text: "Auto-fix CI loop: max 3 attempts, then escalate to user"
  scope: ALWAYS
}

guardrails {
  text: "Clean up branches after merge"
  scope: WRITE_OPS
}

related {
  name: "github-auth"
  relationship: "composes_with"
  description: "Authentication required for all PR operations"
}

related {
  name: "github-code-review"
  relationship: "composes_with"
  description: "Review before merge"
}

meta {
  name: "github-code-review"
  version: "1.1.0"
  summary: "Review PRs — diffs, inline comments via gh or REST"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "code review"
  keywords: "PR review"
  keywords: "pull request review"
  keywords: "inline comments"
  keywords: "review PR"
  intents: "review_pr"
  intents: "review_local_changes"
  intents: "post_review_comment"
  intents: "approve_pr"
  intents: "request_changes"
  patterns: "(review|check) .*(PR|pull request|code|changes)"
  patterns: "PR #(\\d+)"
  patterns: "(approve|request changes|comment on) .*(PR|pull request)"
}

requires {
  env_any: "GITHUB_TOKEN"
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "read_file"
    required: false
  }
  binaries: "git"
}

provides {
  capabilities: "pr_review"
  capabilities: "inline_comments"
  capabilities: "code_quality_check"
}

actions {
  id: "review_local_changes"
  description: "Review local changes before pushing (pre-push review)"
  trigger_phrases: "review my changes"
  trigger_phrases: "review before pushing"
  trigger_phrases: "check the diff"
  trigger_phrases: "code review local"
    rules {
      text: "Always check for: hardcoded secrets, SQL injection, XSS, path traversal in diffs"
      priority: CRITICAL
    }
    rules {
      text: "Start with git diff main...HEAD --stat for scope, then read full diff"
      priority: HIGH
    }
    rules {
      text: "Output format: Critical → Warnings → Suggestions → Looks Good"
      priority: HIGH
    }
    rules {
      text: "Check for debug statements (print, console.log, TODO, FIXME, debugger) left behind"
      priority: HIGH
    }
    rules {
      text: "Use read_file on changed files for full context — diffs alone miss surrounding issues"
      priority: NORMAL
    }
}
actions {
  id: "review_pr"
  description: "Review a pull request on GitHub"
  trigger_phrases: "review PR"
  trigger_phrases: "review pull request"
  trigger_phrases: "look at this PR"
    rules {
      text: "Check out PR locally: git fetch origin pull/<N>/head:pr-<N> && git checkout pr-<N>"
      priority: CRITICAL
    }
    rules {
      text: "Review checklist: Correctness, Security, Code Quality, Testing, Performance, Documentation"
      priority: CRITICAL
    }
    rules {
      text: "gh: gh pr view <N>, gh pr diff <N>. curl: GET /repos/{o}/{r}/pulls/<N>/files"
      priority: HIGH
    }
    rules {
      text: "Run automated checks locally if test suite exists (pytest, npm test, etc.)"
      priority: HIGH
    }
    rules {
      text: "Clean up after review: git checkout main && git branch -D pr-<N>"
      priority: NORMAL
    }
}
actions {
  id: "post_review"
  description: "Submit a formal review — approve, request changes, or comment"
  trigger_phrases: "approve PR"
  trigger_phrases: "request changes"
  trigger_phrases: "submit review"
  trigger_phrases: "post review comment"
    rules {
      text: "Event values: APPROVE (no critical/warning issues), REQUEST_CHANGES (blocking issues), COMMENT (observations only)"
      priority: CRITICAL
    }
    rules {
      text: "gh: gh pr review <N> --approve|--request-changes|--comment --body '...'"
      priority: HIGH
    }
    rules {
      text: "curl: POST /repos/{o}/{r}/pulls/<N>/reviews with commit_id, event, body, comments[]"
      priority: HIGH
    }
    rules {
      text: "Inline comments: specify path, line (in NEW version), side (RIGHT/LEFT), and body"
      priority: HIGH
    }
    rules {
      text: "Post a summary comment in addition to inline reviews for full picture"
      priority: NORMAL
    }
    data {
      key: "review_events"
      list_value {
        items {
          string_value: "APPROVE"
        }
        items {
          string_value: "REQUEST_CHANGES"
        }
        items {
          string_value: "COMMENT"
        }
      }
    }
    data {
      key: "checklist_categories"
      list_value {
        items {
          string_value: "Correctness"
        }
        items {
          string_value: "Security"
        }
        items {
          string_value: "Code Quality"
        }
        items {
          string_value: "Testing"
        }
        items {
          string_value: "Performance"
        }
        items {
          string_value: "Documentation"
        }
      }
    }
    examples {
      label: "approve with gh"
      language: "bash"
      code: "gh pr review 123 --approve --body \"LGTM! Clean code, good tests.\""
    }
    examples {
      label: "request changes with inline comments via curl"
      language: "bash"
      code: "curl -s -X POST \\\n  -H \"Authorization: token $GITHUB_TOKEN\" \\\n  https://api.github.com/repos/$OWNER/$REPO/pulls/123/reviews \\\n  -d '{\n    \"commit_id\": \"'$(curl -s -H \"Authorization: token $GITHUB_TOKEN\" https://api.github.com/repos/$OWNER/$REPO/pulls/123 | python3 -c \"import sys,json; print(json.load(sys.stdin)[chr(104)+chr(101)+chr(97)+chr(100)][chr(115)+chr(104)+chr(97)])\")'\",\n    \"event\": \"REQUEST_CHANGES\",\n    \"body\": \"Found issues — see inline comments.\",\n    \"comments\": [\n      {\"path\": \"src/auth.py\", \"line\": 45, \"side\": \"RIGHT\", \"body\": \"SQL injection risk.\"}\n    ]\n  }'"
    }
}

guardrails {
  text: "Always check out PR locally for full review — don't rely on diff alone"
  scope: ALWAYS
}

guardrails {
  text: "Security check is mandatory: secrets, injection, XSS, path traversal"
  scope: ALWAYS
}

guardrails {
  text: "Clean up PR branches after review"
  scope: ALWAYS
}

related {
  name: "github-auth"
  relationship: "composes_with"
  description: "Authentication required for PR interactions"
}

related {
  name: "github-pr-workflow"
  relationship: "composes_with"
  description: "PR creation and lifecycle management"
}

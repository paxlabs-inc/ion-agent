meta {
  name: "github-issues"
  version: "1.1.0"
  summary: "Create, triage, label, assign GitHub issues via gh or REST"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "github issues"
  keywords: "issue tracker"
  keywords: "bug report"
  keywords: "triage"
  keywords: "issue management"
  intents: "create_issue"
  intents: "list_issues"
  intents: "search_issues"
  intents: "triage_issues"
  intents: "manage_issues"
  patterns: "(create|open|file) .*(issue|bug|ticket)"
  patterns: "(list|show|view|search) .*(issues|tickets)"
  patterns: "(triage|label|assign|close|reopen) .*(issue|ticket)"
  patterns: "gh issue"
}

requires {
  env_any: "GITHUB_TOKEN"
  tools {
    name: "terminal"
    required: true
  }
  binaries: "git"
}

provides {
  capabilities: "issue_management"
  capabilities: "issue_triage"
  capabilities: "issue_bulk_operations"
}

actions {
  id: "view_issues"
  description: "List, view, or search GitHub issues"
  trigger_phrases: "list issues"
  trigger_phrases: "show issues"
  trigger_phrases: "view issue"
  trigger_phrases: "search issues"
    rules {
      text: "gh: gh issue list [--state open] [--label 'bug'] [--assignee @me] [--search '...']"
      priority: HIGH
    }
    rules {
      text: "curl: GET /repos/{o}/{r}/issues?state=open&labels=bug&per_page=20"
      priority: HIGH
    }
    rules {
      text: "GitHub API returns PRs in /issues endpoint — filter with: if 'pull_request' not in item"
      priority: HIGH
    }
    rules {
      text: "Search: GET /search/issues?q=query+repo:owner/repo"
      priority: NORMAL
    }
}
actions {
  id: "create_issue"
  description: "Create a new GitHub issue"
  trigger_phrases: "create issue"
  trigger_phrases: "file a bug"
  trigger_phrases: "open an issue"
  trigger_phrases: "new issue"
    rules {
      text: "gh: gh issue create --title '...' --body '...' --label 'bug,backend' --assignee 'user'"
      priority: HIGH
    }
    rules {
      text: "curl: POST /repos/{o}/{r}/issues with title, body, labels[], assignees[]"
      priority: HIGH
    }
    rules {
      text: "Use templates: Bug Description → Steps to Reproduce → Expected/Actual → Environment"
      priority: NORMAL
    }
    rules {
      text: "Feature Request: Description → Motivation → Proposed Solution → Alternatives"
      priority: NORMAL
    }
    examples {
      label: "create issue with gh"
      language: "bash"
      code: "gh issue create \\\n  --title \"Login redirect ignores ?next= parameter\" \\\n  --body \"## Description\\nAfter logging in, users always land on /dashboard.\" \\\n  --label \"bug,backend\" \\\n  --assignee \"username\""
    }
}
actions {
  id: "manage_issues"
  description: "Label, assign, comment on, close, or reopen issues"
  trigger_phrases: "label issue"
  trigger_phrases: "assign issue"
  trigger_phrases: "close issue"
  trigger_phrases: "comment on issue"
    rules {
      text: "Labels: gh issue edit N --add-label/--remove-label. curl: POST/DELETE /issues/N/labels"
      priority: HIGH
    }
    rules {
      text: "Assign: gh issue edit N --add-assignee user. curl: POST /issues/N/assignees"
      priority: HIGH
    }
    rules {
      text: "Close: gh issue close N. curl: PATCH /issues/N with state='closed'"
      priority: HIGH
    }
    rules {
      text: "Auto-close via PR: include 'Closes #N' or 'Fixes #N' in PR body"
      priority: NORMAL
    }
    data {
      key: "quick_reference"
      map_value {
        entries {
          key: "list"
          string_value: "gh issue list | GET /repos/{o}/{r}/issues"
        }
        entries {
          key: "view"
          string_value: "gh issue view N | GET /repos/{o}/{r}/issues/N"
        }
        entries {
          key: "create"
          string_value: "gh issue create | POST /repos/{o}/{r}/issues"
        }
        entries {
          key: "add_labels"
          string_value: "gh issue edit N --add-label | POST /repos/{o}/{r}/issues/N/labels"
        }
        entries {
          key: "assign"
          string_value: "gh issue edit N --add-assignee | POST /repos/{o}/{r}/issues/N/assignees"
        }
        entries {
          key: "comment"
          string_value: "gh issue comment N | POST /repos/{o}/{r}/issues/N/comments"
        }
        entries {
          key: "close"
          string_value: "gh issue close N | PATCH /repos/{o}/{r}/issues/N"
        }
        entries {
          key: "search"
          string_value: "gh issue list --search | GET /search/issues?q=..."
        }
      }
    }
}
actions {
  id: "triage_issues"
  description: "Triage workflow — categorize, label, and assign untriaged issues"
  trigger_phrases: "triage issues"
  trigger_phrases: "triage backlog"
  trigger_phrases: "sort issues"
    rules {
      text: "Step 1: List untriaged issues (label:needs-triage, state:open)"
      priority: HIGH
    }
    rules {
      text: "Step 2: Read each issue, categorize by type and severity"
      priority: HIGH
    }
    rules {
      text: "Step 3: Apply labels (bug/feature/enhancement + priority)"
      priority: HIGH
    }
    rules {
      text: "Step 4: Assign if owner is clear, comment with triage notes if needed"
      priority: NORMAL
    }
}
actions {
  id: "bulk_operations"
  description: "Batch operations on multiple issues"
  trigger_phrases: "bulk close issues"
  trigger_phrases: "batch update issues"
    rules {
      text: "Combine gh issue list --json number --jq with xargs for batch operations"
      priority: HIGH
    }
    rules {
      text: "curl: list issues → extract numbers → loop PATCH to close each"
      priority: NORMAL
    }
}

guardrails {
  text: "Filter out PRs from /issues results — GitHub mixes them together"
  scope: ALWAYS
}

guardrails {
  text: "Never bulk-close without user confirmation on the list of affected issues"
  scope: WRITE_OPS
}

related {
  name: "github-auth"
  relationship: "composes_with"
  description: "Authentication required"
}

related {
  name: "github-pr-workflow"
  relationship: "composes_with"
  description: "Issues link to PRs via Closes/Fixes keywords"
}

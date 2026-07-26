meta {
  name: "github-repo-management"
  version: "1.1.0"
  summary: "Clone, create, fork repos — manage remotes, releases, secrets"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "github repo"
  keywords: "repository"
  keywords: "clone"
  keywords: "fork"
  keywords: "release"
  keywords: "secrets"
  keywords: "branch protection"
  keywords: "gist"
  keywords: "workflow"
  intents: "clone_repo"
  intents: "create_repo"
  intents: "fork_repo"
  intents: "manage_settings"
  intents: "manage_releases"
  intents: "manage_secrets"
  intents: "manage_workflows"
  patterns: "(clone|create|fork) .*(repo|repository)"
  patterns: "(manage|edit|configure) .*(repo|settings|branch protection)"
  patterns: "(create|list|download) .*(release|releases)"
  patterns: "(set|list|delete) .*(secret|secrets)"
  patterns: "(list|run|rerun) .*(workflow|action|CI)"
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
  capabilities: "repo_management"
  capabilities: "release_management"
  capabilities: "secrets_management"
  capabilities: "workflow_management"
  capabilities: "gist_management"
}

actions {
  id: "clone_repo"
  description: "Clone a GitHub repository"
  trigger_phrases: "clone repo"
  trigger_phrases: "clone a repository"
  trigger_phrases: "download repo"
    rules {
      text: "git clone https://github.com/owner/repo.git (works with credential helper)"
      priority: HIGH
    }
    rules {
      text: "gh: gh repo clone owner/repo [-- --depth 1] for shallow clone"
      priority: HIGH
    }
    rules {
      text: "Options: --depth 1 (shallow), --branch develop (specific branch), --single-branch"
      priority: NORMAL
    }
}
actions {
  id: "create_repo"
  description: "Create a new GitHub repository"
  trigger_phrases: "create repo"
  trigger_phrases: "new repository"
  trigger_phrases: "create a project"
    rules {
      text: "gh: gh repo create name --public|--private --clone [--description '...'] [--license MIT]"
      priority: HIGH
    }
    rules {
      text: "curl: POST /user/repos with name, description, private, auto_init, license_template"
      priority: HIGH
    }
    rules {
      text: "From existing dir: gh repo create name --source . --public --push"
      priority: HIGH
    }
    rules {
      text: "From template: gh repo create name --template owner/template --public --clone"
      priority: NORMAL
    }
    examples {
      label: "create and clone repo"
      language: "bash"
      code: "gh repo create my-project --public --clone --description \"A useful tool\""
    }
    examples {
      label: "push existing directory"
      language: "bash"
      code: "cd /path/to/project\ngh repo create my-project --source . --public --push"
    }
}
actions {
  id: "fork_repo"
  description: "Fork and sync a repository"
  trigger_phrases: "fork repo"
  trigger_phrases: "fork a repository"
  trigger_phrases: "sync fork"
    rules {
      text: "gh: gh repo fork owner/repo --clone"
      priority: HIGH
    }
    rules {
      text: "curl: POST /repos/{o}/{r}/forks → wait → clone → add upstream remote"
      priority: HIGH
    }
    rules {
      text: "Sync: git fetch upstream && git merge upstream/main && git push origin main. Or: gh repo sync"
      priority: NORMAL
    }
}
actions {
  id: "manage_settings"
  description: "View and edit repository settings"
  trigger_phrases: "repo settings"
  trigger_phrases: "edit repo"
  trigger_phrases: "branch protection"
  trigger_phrases: "repo info"
    rules {
      text: "gh: gh repo edit --description/--visibility/--enable-wiki/--default-branch/--add-topic"
      priority: HIGH
    }
    rules {
      text: "curl: PATCH /repos/{o}/{r} for settings, PUT /repos/{o}/{r}/branches/main/protection for branch rules"
      priority: HIGH
    }
    rules {
      text: "View: gh repo view, gh repo list, gh search repos"
      priority: NORMAL
    }
}
actions {
  id: "manage_releases"
  description: "Create, list, and manage releases"
  trigger_phrases: "create release"
  trigger_phrases: "list releases"
  trigger_phrases: "publish release"
  trigger_phrases: "download release"
    rules {
      text: "gh: gh release create v1.0.0 --title '...' --generate-notes [--draft] [--prerelease]"
      priority: HIGH
    }
    rules {
      text: "curl: POST /repos/{o}/{r}/releases with tag_name, name, body, generate_release_notes"
      priority: HIGH
    }
    rules {
      text: "Upload assets: gh release create v1.0 ./dist/binary. Or curl with Content-Type: application/octet-stream"
      priority: NORMAL
    }
    examples {
      label: "create release with auto-generated notes"
      language: "bash"
      code: "gh release create v1.0.0 --title \"v1.0.0\" --generate-notes"
    }
}
actions {
  id: "manage_secrets"
  description: "Manage GitHub Actions secrets"
  trigger_phrases: "set secret"
  trigger_phrases: "list secrets"
  trigger_phrases: "delete secret"
  trigger_phrases: "github actions secret"
    rules {
      text: "gh is dramatically simpler for secrets — recommend installing gh just for this if not available"
      priority: CRITICAL
    }
    rules {
      text: "gh: gh secret set KEY --body 'value', gh secret list, gh secret delete KEY"
      priority: HIGH
    }
    rules {
      text: "curl requires encryption: get public key → encrypt with PyNaCl → PUT encrypted value"
      priority: HIGH
    }
    rules {
      text: "Read from file: gh secret set SSH_KEY < ~/.ssh/id_rsa"
      priority: NORMAL
    }
}
actions {
  id: "manage_workflows"
  description: "List, trigger, and manage GitHub Actions workflows"
  trigger_phrases: "list workflows"
  trigger_phrases: "run workflow"
  trigger_phrases: "rerun CI"
  trigger_phrases: "github actions"
    rules {
      text: "gh: gh workflow list, gh run list, gh run view <ID>, gh run rerun <ID>"
      priority: HIGH
    }
    rules {
      text: "curl: GET /repos/{o}/{r}/actions/workflows, /actions/runs. POST /actions/runs/<ID>/rerun"
      priority: HIGH
    }
    rules {
      text: "Manual trigger: gh workflow run ci.yml --ref main. Or POST /workflows/<ID>/dispatches"
      priority: NORMAL
    }
}
actions {
  id: "manage_gists"
  description: "Create and list GitHub gists"
  trigger_phrases: "create gist"
  trigger_phrases: "list gists"
  trigger_phrases: "share code snippet"
    rules {
      text: "gh: gh gist create file.py --public --desc '...'"
      priority: HIGH
    }
    rules {
      text: "curl: POST /gists with description, public, files{}"
      priority: HIGH
    }
}

guardrails {
  text: "For secrets management, prefer gh CLI — curl requires complex encryption"
  scope: ALWAYS
}

guardrails {
  text: "Never auto-fork without confirming with user — forks have implications"
  scope: WRITE_OPS
}

guardrails {
  text: "Verify branch protection rules before allowing merges in shared repos"
  scope: ALWAYS
}

related {
  name: "github-auth"
  relationship: "composes_with"
  description: "Authentication required for all operations"
}

related {
  name: "github-pr-workflow"
  relationship: "composes_with"
  description: "PRs and branches use repos"
}

related {
  name: "github-issues"
  relationship: "composes_with"
  description: "Issues live in repos"
}

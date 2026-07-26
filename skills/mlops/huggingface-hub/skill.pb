meta {
  name: "huggingface-hub"
  version: "1.0.0"
  summary: "HuggingFace hf CLI: search/download/upload models, datasets, and Spaces"
  author: "Hugging Face"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "huggingface"
  keywords: "hf"
  keywords: "hugging face"
  keywords: "hf cli"
  keywords: "download model"
  keywords: "upload model"
  keywords: "hf hub"
  keywords: "huggingface-cli"
  intents: "download_model"
  intents: "upload_model"
  intents: "search_models"
  intents: "manage_repos"
  intents: "hf_auth"
  patterns: "(download|upload|search|list) .*(model|dataset|space) .*(hugging ?face|hf)"
  patterns: "hf (download|upload|auth|repos|datasets|models)"
  patterns: "huggingface"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
}

provides {
  capabilities: "model_management"
  capabilities: "dataset_management"
  capabilities: "repo_management"
  capabilities: "hub_interaction"
}

actions {
  id: "install"
  description: "Install the hf CLI"
  trigger_phrases: "install hf cli"
  trigger_phrases: "set up huggingface cli"
  trigger_phrases: "install huggingface-cli"
    rules {
      text: "The hf command replaces the deprecated huggingface-cli command"
      priority: CRITICAL
    }
    rules {
      text: "Install: curl -LsSf https://hf.co/cli/install.sh | bash -s"
      priority: HIGH
    }
    data {
      key: "install_command"
      string_value: "curl -LsSf https://hf.co/cli/install.sh | bash -s"
    }
}
actions {
  id: "authentication"
  description: "Login and manage HuggingFace tokens"
  trigger_phrases: "login to huggingface"
  trigger_phrases: "hf auth"
  trigger_phrases: "set huggingface token"
    rules {
      text: "Recommended: set HF_TOKEN env var or use --token flag"
      priority: CRITICAL
    }
    rules {
      text: "Get tokens from huggingface.co/settings/tokens"
      priority: HIGH
    }
    rules {
      text: "Commands: hf auth login, logout, list, switch, whoami"
      priority: NORMAL
    }
    data {
      key: "auth_commands"
      map_value {
        entries {
          key: "login"
          string_value: "hf auth login"
        }
        entries {
          key: "logout"
          string_value: "hf auth logout"
        }
        entries {
          key: "whoami"
          string_value: "hf auth whoami"
        }
        entries {
          key: "list"
          string_value: "hf auth list"
        }
      }
    }
}
actions {
  id: "download"
  description: "Download models, datasets, or files from HuggingFace Hub"
  trigger_phrases: "download model"
  trigger_phrases: "download dataset"
  trigger_phrases: "get model from huggingface"
  trigger_phrases: "hf download"
    rules {
      text: "Use hf download REPO_ID for models and datasets"
      priority: HIGH
    }
    rules {
      text: "Use --format json for machine-readable output"
      priority: HIGH
    }
    rules {
      text: "Use -q/--quiet for IDs-only output"
      priority: NORMAL
    }
    examples {
      label: "download a model"
      language: "bash"
      code: "hf download meta-llama/Llama-2-7b-hf"
    }
    examples {
      label: "download a dataset"
      language: "bash"
      code: "hf download squad"
    }
}
actions {
  id: "upload"
  description: "Upload files or folders to HuggingFace Hub"
  trigger_phrases: "upload model"
  trigger_phrases: "push to huggingface"
  trigger_phrases: "upload dataset"
  trigger_phrases: "hf upload"
    rules {
      text: "hf upload REPO_ID for single-commit uploads"
      priority: HIGH
    }
    rules {
      text: "hf upload-large-folder REPO_ID LOCAL_PATH for resumable large directory uploads"
      priority: HIGH
    }
    rules {
      text: "Use hf sync for syncing local directory with bucket"
      priority: NORMAL
    }
    data {
      key: "upload_commands"
      map_value {
        entries {
          key: "upload"
          string_value: "hf upload REPO_ID"
        }
        entries {
          key: "upload_large"
          string_value: "hf upload-large-folder REPO_ID LOCAL_PATH"
        }
        entries {
          key: "sync"
          string_value: "hf sync"
        }
      }
    }
}
actions {
  id: "repo_management"
  description: "Create, delete, and manage HuggingFace repositories"
  trigger_phrases: "create repo"
  trigger_phrases: "manage repos"
  trigger_phrases: "hf repos"
  trigger_phrases: "duplicate repo"
    rules {
      text: "Commands: hf repos create, delete, duplicate, move, branch, tag, delete-files"
      priority: HIGH
    }
    rules {
      text: "Use hf repos duplicate to clone a model/dataset/Space to a new ID"
      priority: NORMAL
    }
    data {
      key: "repo_commands"
      map_value {
        entries {
          key: "create"
          string_value: "hf repos create"
        }
        entries {
          key: "delete"
          string_value: "hf repos delete"
        }
        entries {
          key: "duplicate"
          string_value: "hf repos duplicate"
        }
        entries {
          key: "move"
          string_value: "hf repos move"
        }
      }
    }
}
actions {
  id: "specialized_commands"
  description: "Datasets, models, discussions, compute, and infrastructure"
  trigger_phrases: "hf datasets"
  trigger_phrases: "hf models"
  trigger_phrases: "hf discussions"
  trigger_phrases: "hf endpoints"
  trigger_phrases: "hf jobs"
    rules {
      text: "Datasets: hf datasets list, info, parquet, sql SQL (DuckDB queries)"
      priority: HIGH
    }
    rules {
      text: "Discussions: list, create, comment, close, reopen, merge PRs"
      priority: HIGH
    }
    rules {
      text: "Endpoints: deploy, pause, resume, scale-to-zero for inference"
      priority: HIGH
    }
    rules {
      text: "Jobs: hf jobs uv for running Python with inline deps on HF infra"
      priority: NORMAL
    }
    rules {
      text: "Cache: hf cache list, prune (remove detached revisions), verify"
      priority: NORMAL
    }
    data {
      key: "command_categories"
      map_value {
        entries {
          key: "datasets"
          string_value: "list, info, parquet, sql"
        }
        entries {
          key: "models"
          string_value: "list, info"
        }
        entries {
          key: "papers"
          string_value: "list daily papers"
        }
        entries {
          key: "discussions"
          string_value: "list, create, comment, close, merge"
        }
        entries {
          key: "endpoints"
          string_value: "deploy, pause, resume, scale-to-zero"
        }
        entries {
          key: "jobs"
          string_value: "uv, stats"
        }
        entries {
          key: "cache"
          string_value: "list, prune, verify"
        }
        entries {
          key: "webhooks"
          string_value: "create, watch, enable/disable"
        }
      }
    }
}

guardrails {
  text: "The hf command replaces huggingface-cli — always use hf"
  scope: ALWAYS
}

guardrails {
  text: "Use --format json for machine-readable output in scripts"
  scope: ALWAYS
}

guardrails {
  text: "Authentication: prefer HF_TOKEN env var over interactive login"
  scope: AUTH_OPS
}

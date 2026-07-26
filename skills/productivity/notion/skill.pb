meta {
  name: "notion"
  version: "2.0.0"
  summary: "Notion API + ntn CLI — pages, databases, markdown, Workers"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "notion"
  keywords: "ntn"
  keywords: "notion api"
  keywords: "notion page"
  keywords: "notion database"
  keywords: "notion worker"
  intents: "notion_read"
  intents: "notion_write"
  intents: "notion_search"
  intents: "notion_query"
  intents: "notion_upload"
  intents: "notion_worker"
  patterns: "(read|write|create|update|search|query) .*(notion|page|database)"
  patterns: "notion .*(page|database|worker|api)"
  patterns: "upload .*(notion|file)"
}

requires {
  env_all: "NOTION_API_KEY"
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: false
  }
  binaries: "curl"
}

provides {
  capabilities: "notion_read"
  capabilities: "notion_write"
  capabilities: "notion_search"
  capabilities: "notion_database"
  capabilities: "notion_workers"
  capabilities: "notion_markdown"
  output_types: ".md"
}

actions {
  id: "setup"
  description: "First-time Notion setup — get token and install CLI"
  trigger_phrases: "set up notion"
  trigger_phrases: "configure notion"
  trigger_phrases: "notion setup"
    rules {
      text: "Create integration at https://notion.so/my-integrations, copy API key"
      priority: CRITICAL
    }
    rules {
      text: "Store NOTION_API_KEY in ${ION_HOME:-~/.ion}/.env"
      priority: CRITICAL
    }
    rules {
      text: "Share target pages/databases with the integration in Notion (page menu ... → Connect to). Without this, API returns 404."
      priority: CRITICAL
    }
    rules {
      text: "Install ntn CLI: curl -fsSL https://ntn.dev | bash (macOS/Linux only)"
      priority: HIGH
    }
    rules {
      text: "Skip ntn login — use integration token directly: NOTION_API_TOKEN=$NOTION_API_KEY, NOTION_KEYRING=0"
      priority: HIGH
    }
    data {
      key: "env_setup"
      list_value {
        items {
          string_value: "NOTION_API_KEY=ntn_your_key_here"
        }
        items {
          string_value: "NOTION_API_TOKEN=$NOTION_API_KEY"
        }
        items {
          string_value: "NOTION_KEYRING=0"
        }
      }
    }
    data {
      key: "api_version"
      string_value: "2025-09-03"
    }
    data {
      key: "ntn_install"
      string_value: "curl -fsSL https://ntn.dev | bash"
    }
}
actions {
  id: "search"
  description: "Search Notion for pages or databases"
  trigger_phrases: "search notion"
  trigger_phrases: "find in notion"
  trigger_phrases: "look up in notion"
    rules {
      text: "ntn: ntn api v1/search query='page title'"
      priority: HIGH
    }
    rules {
      text: "curl: POST /v1/search with {query: '...'}"
      priority: HIGH
    }
    data {
      key: "ntn_command"
      string_value: "ntn api v1/search query='PAGE_TITLE'"
    }
    data {
      key: "curl_endpoint"
      string_value: "POST https://api.notion.com/v1/search"
    }
}
actions {
  id: "read_page"
  description: "Read a Notion page — metadata, markdown, or blocks"
  trigger_phrases: "read notion page"
  trigger_phrases: "get notion page"
  trigger_phrases: "open notion page"
    rules {
      text: "For agent-friendly content: use /markdown endpoint (returns readable text)"
      priority: HIGH
    }
    rules {
      text: "For structured content: use /blocks/{id}/children (returns block JSON)"
      priority: HIGH
    }
    rules {
      text: "ntn: ntn api v1/pages/{id}/markdown"
      priority: NORMAL
    }
    rules {
      text: "Page/database IDs are UUIDs (with or without dashes accepted)"
      priority: NORMAL
    }
    data {
      key: "endpoints"
      map_value {
        entries {
          key: "metadata"
          string_value: "GET /v1/pages/{page_id}"
        }
        entries {
          key: "markdown"
          string_value: "GET /v1/pages/{page_id}/markdown"
        }
        entries {
          key: "blocks"
          string_value: "GET /v1/blocks/{page_id}/children"
        }
      }
    }
}
actions {
  id: "create_page"
  description: "Create a new Notion page"
  trigger_phrases: "create notion page"
  trigger_phrases: "new notion page"
  trigger_phrases: "add page to notion"
    rules {
      text: "Parent is required — either page_id or database_id"
      priority: CRITICAL
    }
    rules {
      text: "POST /v1/pages accepts markdown body param for content"
      priority: HIGH
    }
    rules {
      text: "For database pages, properties must match the database schema"
      priority: HIGH
    }
    rules {
      text: "Use is_inline: true when creating data sources to embed in a page"
      priority: NORMAL
    }
    data {
      key: "endpoint"
      string_value: "POST https://api.notion.com/v1/pages"
    }
    data {
      key: "property_formats"
      map_value {
        entries {
          key: "title"
          string_value: "{\"title\":[{\"text\":{\"content\":\"...\"}}]}"
        }
        entries {
          key: "rich_text"
          string_value: "{\"rich_text\":[{\"text\":{\"content\":\"...\"}}]}"
        }
        entries {
          key: "select"
          string_value: "{\"select\":{\"name\":\"Option\"}}"
        }
        entries {
          key: "multi_select"
          string_value: "{\"multi_select\":[{\"name\":\"A\"},{\"name\":\"B\"}]}"
        }
        entries {
          key: "date"
          string_value: "{\"date\":{\"start\":\"2026-01-15\",\"end\":\"2026-01-16\"}}"
        }
        entries {
          key: "checkbox"
          string_value: "{\"checkbox\":true}"
        }
        entries {
          key: "number"
          string_value: "{\"number\":42}"
        }
        entries {
          key: "url"
          string_value: "{\"url\":\"https://...\"}"
        }
        entries {
          key: "relation"
          string_value: "{\"relation\":[{\"id\":\"page_id\"}]}"
        }
      }
    }
}
actions {
  id: "update_page"
  description: "Update an existing Notion page or its properties"
  trigger_phrases: "update notion page"
  trigger_phrases: "edit notion page"
  trigger_phrases: "change notion page"
    rules {
      text: "PATCH /v1/pages/{page_id} for properties, PATCH /v1/pages/{page_id}/markdown for content"
      priority: HIGH
    }
    data {
      key: "endpoints"
      map_value {
        entries {
          key: "properties"
          string_value: "PATCH /v1/pages/{page_id}"
        }
        entries {
          key: "markdown"
          string_value: "PATCH /v1/pages/{page_id}/markdown"
        }
      }
    }
}
actions {
  id: "query_database"
  description: "Query a Notion database (data source)"
  trigger_phrases: "query notion database"
  trigger_phrases: "search notion database"
  trigger_phrases: "filter notion"
    rules {
      text: "As of API 2025-09-03, databases are called 'data sources'. Use /data_sources/ endpoints."
      priority: CRITICAL
    }
    rules {
      text: "Two IDs per database: database_id (for creating pages) and data_source_id (for querying)"
      priority: HIGH
    }
    rules {
      text: "ntn: ntn api v1/data_sources/{id}/query -X POST filter[property]=Status filter[select][equals]=Active"
      priority: HIGH
    }
    rules {
      text: "For complex queries with sorts/compound filters, pipe JSON through --json flag"
      priority: NORMAL
    }
    data {
      key: "endpoint"
      string_value: "POST /v1/data_sources/{data_source_id}/query"
    }
    data {
      key: "id_mapping"
      map_value {
        entries {
          key: "create_pages_with"
          string_value: "database_id"
        }
        entries {
          key: "query_with"
          string_value: "data_source_id"
        }
      }
    }
}
actions {
  id: "upload_file"
  description: "Upload a file to Notion"
  trigger_phrases: "upload to notion"
  trigger_phrases: "attach file to notion"
  trigger_phrases: "add file to notion"
    rules {
      text: "ntn CLI: ntn files create < photo.png (one-liner, preferred)"
      priority: HIGH
    }
    rules {
      text: "HTTP: 3-step flow — create upload → PUT bytes → reference file_upload_id"
      priority: HIGH
    }
    data {
      key: "ntn_command"
      string_value: "ntn files create < photo.png"
    }
    data {
      key: "curl_flow"
      list_value {
        items {
          string_value: "1. POST /v1/file_uploads {filename, content_type}"
        }
        items {
          string_value: "2. PUT {upload_url} --data-binary @file"
        }
        items {
          string_value: "3. Reference {file_upload_id} in page/block payload"
        }
      }
    }
}
actions {
  id: "manage_workers"
  description: "Build or manage Notion Workers (syncs, tools, webhooks)"
  trigger_phrases: "notion worker"
  trigger_phrases: "create worker"
  trigger_phrases: "deploy worker"
  trigger_phrases: "notion sync"
  trigger_phrases: "notion webhook"
  needs_env: "NOTION_API_KEY"
    rules {
      text: "Workers require Business or Enterprise plan"
      priority: CRITICAL
    }
    rules {
      text: "ntn CLI required — macOS/Linux only. Windows users need WSL2"
      priority: CRITICAL
    }
    rules {
      text: "Scaffold: ntn workers new <name>. Code in src/index.ts. Deploy: ntn workers deploy"
      priority: HIGH
    }
    rules {
      text: "Workers can expose: syncs (scheduled data pulls), tools (callable from Notion agents), webhooks (HTTP event receivers)"
      priority: HIGH
    }
    rules {
      text: "Set secrets with ntn workers env set. Webhook URLs are generated by Notion after deploy"
      priority: NORMAL
    }
    data {
      key: "lifecycle_commands"
      map_value {
        entries {
          key: "scaffold"
          string_value: "ntn workers new <name>"
        }
        entries {
          key: "deploy"
          string_value: "ntn workers deploy"
        }
        entries {
          key: "list"
          string_value: "ntn workers list"
        }
        entries {
          key: "execute"
          string_value: "ntn workers exec <key> -d '{...}'"
        }
        entries {
          key: "logs"
          string_value: "ntn workers runs logs <run-id>"
        }
        entries {
          key: "env_set"
          string_value: "ntn workers env set KEY=VALUE"
        }
      }
    }
}
actions {
  id: "choose_path"
  description: "Decide between ntn CLI and curl for a Notion task"
  trigger_phrases: "how to use notion"
  trigger_phrases: "notion cli vs curl"
    rules {
      text: "ntn CLI: preferred on macOS/Linux. Concise syntax, one-liner uploads, required for Workers"
      priority: HIGH
    }
    rules {
      text: "curl: cross-platform fallback. Works on Windows. Use when ntn not installed"
      priority: HIGH
    }
    rules {
      text: "Runtime: if command -v ntn >/dev/null 2>&1; then use ntn; else use curl; fi"
      priority: NORMAL
    }
    data {
      key: "path_matrix"
      map_value {
        entries {
          key: "read_write_pages"
          string_value: "ntn api ... | curl"
        }
        entries {
          key: "read_page_for_agent"
          string_value: "ntn api v1/pages/{id}/markdown | curl /markdown"
        }
        entries {
          key: "upload_file"
          string_value: "ntn files create < file | 3-step HTTP flow"
        }
        entries {
          key: "workers"
          string_value: "ntn workers ... (CLI only)"
        }
      }
    }
}

guardrails {
  text: "Always pass Notion-Version: 2025-09-03 on HTTP requests"
  scope: ALWAYS
}

guardrails {
  text: "Share pages/databases with integration before API access — 404 means not shared, not missing"
  scope: ALWAYS
}

guardrails {
  text: "Rate limit ~3 req/s average — CLI doesn't bypass"
  scope: ALWAYS
}

guardrails {
  text: "Pass -s to curl to suppress progress bars"
  scope: READ_OPS
}

meta {
  name: "google-workspace"
  version: "1.1.0"
  summary: "Gmail, Calendar, Drive, Docs, Sheets via gws CLI or Python"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "gmail"
  keywords: "google calendar"
  keywords: "google drive"
  keywords: "google docs"
  keywords: "google sheets"
  keywords: "google contacts"
  keywords: "gws"
  keywords: "google workspace"
  intents: "gmail_read"
  intents: "gmail_send"
  intents: "gmail_reply"
  intents: "calendar_list"
  intents: "calendar_create"
  intents: "drive_search"
  intents: "drive_upload"
  intents: "drive_download"
  intents: "sheets_read"
  intents: "sheets_write"
  intents: "docs_read"
  intents: "docs_create"
  patterns: "(read|send|search|list|create|upload|download) .*(gmail|email|calendar|drive|sheet|doc|contact)"
  patterns: "google .*(mail|calendar|drive|docs|sheets|contacts)"
  patterns: "(upcoming|next) .*(meeting|event|calendar)"
}

requires {
  env_all: "GOOGLE_TOKEN_PATH"
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: false
  }
  binaries: "python3"
}

provides {
  capabilities: "gmail_read"
  capabilities: "gmail_send"
  capabilities: "gmail_reply"
  capabilities: "gmail_search"
  capabilities: "calendar_list"
  capabilities: "calendar_create"
  capabilities: "calendar_delete"
  capabilities: "drive_search"
  capabilities: "drive_upload"
  capabilities: "drive_download"
  capabilities: "drive_share"
  capabilities: "sheets_read"
  capabilities: "sheets_write"
  capabilities: "sheets_append"
  capabilities: "docs_read"
  capabilities: "docs_create"
  capabilities: "docs_append"
  capabilities: "contacts_list"
  output_types: ".json"
  output_types: ".pdf"
  output_types: ".csv"
}

actions {
  id: "setup"
  description: "First-time Google Workspace OAuth2 setup"
  trigger_phrases: "set up google workspace"
  trigger_phrases: "configure gmail"
  trigger_phrases: "google oauth setup"
  trigger_phrases: "authorize google"
    rules {
      text: "Check if already set up first: setup.py --check — if AUTHENTICATED, skip to usage"
      priority: CRITICAL
    }
    rules {
      text: "Ask user TWO questions before setup: (1) which services needed — email-only should use himalaya skill instead, (2) does account use Advanced Protection"
      priority: CRITICAL
    }
    rules {
      text: "OAuth client: Desktop app type, enable Gmail/Calendar/Drive/Sheets/Docs/People APIs"
      priority: CRITICAL
    }
    rules {
      text: "Store client secret JSON, run --client-secret, get --auth-url, user pastes redirect URL, run --auth-code"
      priority: HIGH
    }
    rules {
      text: "If Error 403: access_denied → user must add themselves as test user at console.cloud.google.com/auth/audience"
      priority: HIGH
    }
    rules {
      text: "Token stored at ~/.ion/google_token.json — auto-refreshes"
      priority: NORMAL
    }
    data {
      key: "setup_script"
      string_value: "python ${ION_HOME:-$HOME/.ion}/skills/productivity/google-workspace/scripts/setup.py"
    }
    data {
      key: "api_script"
      string_value: "python ${ION_HOME:-$HOME/.ion}/skills/productivity/google-workspace/scripts/google_api.py"
    }
    data {
      key: "required_apis"
      list_value {
        items {
          string_value: "Gmail API"
        }
        items {
          string_value: "Google Calendar API"
        }
        items {
          string_value: "Google Drive API"
        }
        items {
          string_value: "Google Sheets API"
        }
        items {
          string_value: "Google Docs API"
        }
        items {
          string_value: "People API"
        }
      }
    }
}
actions {
  id: "gmail_search"
  description: "Search Gmail messages"
  trigger_phrases: "search gmail"
  trigger_phrases: "find email"
  trigger_phrases: "check inbox"
  trigger_phrases: "unread emails"
    rules {
      text: "Returns JSON array with id, threadId, from, to, subject, date, snippet, labels"
      priority: HIGH
    }
    rules {
      text: "Use Gmail search syntax: is:unread, from:, newer_than:, has:attachment, filename:"
      priority: HIGH
    }
    rules {
      text: "Load gmail-search-syntax.md reference for complex queries"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "$GAPI gmail search \"QUERY\" --max N"
    }
    examples {
      label: "search unread emails"
      language: "bash"
      code: "$GAPI gmail search \"is:unread\" --max 10"
    }
    examples {
      label: "search with date filter"
      language: "bash"
      code: "$GAPI gmail search \"from:boss@company.com newer_than:1d\""
    }
}
actions {
  id: "gmail_read"
  description: "Read a full Gmail message"
  trigger_phrases: "read email"
  trigger_phrases: "get email"
  trigger_phrases: "open email"
    rules {
      text: "Returns full message with body text in JSON"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "$GAPI gmail get MESSAGE_ID"
    }
}
actions {
  id: "gmail_send"
  description: "Send or reply to Gmail messages"
  trigger_phrases: "send email"
  trigger_phrases: "reply to email"
  trigger_phrases: "compose email"
    rules {
      text: "NEVER send email without confirming recipients, subject, and body with user first"
      priority: CRITICAL
    }
    rules {
      text: "Reply auto-threads and sets In-Reply-To header"
      priority: HIGH
    }
    rules {
      text: "Support --html flag for HTML body and --from for custom sender"
      priority: NORMAL
    }
    examples {
      label: "send a plain text email"
      language: "bash"
      code: "$GAPI gmail send --to user@example.com --subject \"Hello\" --body \"Message text\""
    }
    examples {
      label: "reply to a message"
      language: "bash"
      code: "$GAPI gmail reply MESSAGE_ID --body \"Thanks, that works for me.\""
    }
}
actions {
  id: "calendar_ops"
  description: "List, create, or delete calendar events"
  trigger_phrases: "list events"
  trigger_phrases: "create event"
  trigger_phrases: "calendar"
  trigger_phrases: "upcoming meetings"
  trigger_phrases: "schedule meeting"
    rules {
      text: "NEVER create/delete calendar events without confirming with user first"
      priority: CRITICAL
    }
    rules {
      text: "Times must include timezone — always ISO 8601 with offset (e.g., 2026-03-01T10:00:00-06:00) or UTC (Z)"
      priority: CRITICAL
    }
    rules {
      text: "Default list shows next 7 days — use --start/--end for custom range"
      priority: HIGH
    }
    data {
      key: "command_patterns"
      map_value {
        entries {
          key: "list"
          string_value: "$GAPI calendar list [--start ISO --end ISO]"
        }
        entries {
          key: "create"
          string_value: "$GAPI calendar create --summary TITLE --start ISO --end ISO"
        }
        entries {
          key: "delete"
          string_value: "$GAPI calendar delete EVENT_ID"
        }
      }
    }
}
actions {
  id: "drive_ops"
  description: "Search, upload, download, share, or delete Drive files"
  trigger_phrases: "upload to drive"
  trigger_phrases: "download from drive"
  trigger_phrases: "search drive"
  trigger_phrases: "share file"
  trigger_phrases: "google drive"
    rules {
      text: "NEVER delete Drive files or share files without confirming with user first"
      priority: CRITICAL
    }
    rules {
      text: "Delete defaults to trash (reversible) — use --permanent only when explicitly requested"
      priority: HIGH
    }
    rules {
      text: "Google-native files export to sensible defaults: Docs→pdf, Sheets→csv, Slides→pdf"
      priority: HIGH
    }
    rules {
      text: "Auto-detects MIME type on upload"
      priority: NORMAL
    }
    data {
      key: "command_patterns"
      map_value {
        entries {
          key: "search"
          string_value: "$GAPI drive search \"QUERY\" --max N"
        }
        entries {
          key: "upload"
          string_value: "$GAPI drive upload /path/to/file [--name NAME] [--parent FOLDER_ID]"
        }
        entries {
          key: "download"
          string_value: "$GAPI drive download FILE_ID [--output path]"
        }
        entries {
          key: "share"
          string_value: "$GAPI drive share FILE_ID --email EMAIL --role reader|writer"
        }
        entries {
          key: "delete"
          string_value: "$GAPI drive delete FILE_ID [--permanent]"
        }
      }
    }
}
actions {
  id: "sheets_ops"
  description: "Read, create, or write Google Sheets"
  trigger_phrases: "read sheet"
  trigger_phrases: "create spreadsheet"
  trigger_phrases: "update sheet"
  trigger_phrases: "google sheets"
    rules {
      text: "NEVER modify sheets without confirming with user first"
      priority: CRITICAL
    }
    rules {
      text: "Range format: Sheet1!A1:D10"
      priority: HIGH
    }
    data {
      key: "command_patterns"
      map_value {
        entries {
          key: "get"
          string_value: "$GAPI sheets get SHEET_ID \"Sheet1!A1:D10\""
        }
        entries {
          key: "create"
          string_value: "$GAPI sheets create --title \"TITLE\""
        }
        entries {
          key: "update"
          string_value: "$GAPI sheets update SHEET_ID \"RANGE\" --values '[[...]]'"
        }
        entries {
          key: "append"
          string_value: "$GAPI sheets append SHEET_ID \"RANGE\" --values '[[...]]'"
        }
      }
    }
}
actions {
  id: "docs_ops"
  description: "Read, create, or append to Google Docs"
  trigger_phrases: "read doc"
  trigger_phrases: "create document"
  trigger_phrases: "google docs"
  trigger_phrases: "append to doc"
    rules {
      text: "NEVER create/modify docs without confirming with user first"
      priority: CRITICAL
    }
    rules {
      text: "Returns JSON with documentId, title, url"
      priority: HIGH
    }
    data {
      key: "command_patterns"
      map_value {
        entries {
          key: "get"
          string_value: "$GAPI docs get DOC_ID"
        }
        entries {
          key: "create"
          string_value: "$GAPI docs create --title \"TITLE\" [--body \"text\"]"
        }
        entries {
          key: "append"
          string_value: "$GAPI docs append DOC_ID --text \"content\""
        }
      }
    }
}

guardrails {
  text: "Always check auth before first use — run setup.py --check"
  scope: ALWAYS
}

guardrails {
  text: "Confirm all write operations (send email, create/delete events, delete files, share, modify docs/sheets) with user before executing"
  scope: WRITE_OPS
}

guardrails {
  text: "Calendar times must include timezone — always ISO 8601 with offset"
  scope: ALWAYS
}

guardrails {
  text: "Respect rate limits — avoid rapid-fire sequential API calls"
  scope: ALWAYS
}

guardrails {
  text: "Prefer himalaya skill for email-only use cases — no Google Cloud project needed"
  scope: ALWAYS
}

related {
  name: "himalaya"
  relationship: "alternative_to"
  description: "Email-only alternative using Gmail App Password — simpler setup"
}

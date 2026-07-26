meta {
  name: "obsidian"
  version: "1.0.0"
  summary: "Read, search, create, and edit notes in an Obsidian vault"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "obsidian"
  keywords: "vault"
  keywords: "obsidian note"
  keywords: "markdown note"
  keywords: "wikilink"
  intents: "read_note"
  intents: "search_notes"
  intents: "create_note"
  intents: "edit_note"
  intents: "append_note"
  patterns: "(read|open|show) .*(obsidian|vault|note)"
  patterns: "(search|find|look up) .*(obsidian|vault|note)"
  patterns: "(create|add|new) .*(obsidian|note)"
  patterns: "obsidian"
}

requires {
  tools {
    name: "read_file"
    required: true
  }
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "search_files"
    required: true
  }
  tools {
    name: "patch"
    required: false
  }
  tools {
    name: "terminal"
    required: false
  }
}

provides {
  capabilities: "obsidian_read"
  capabilities: "obsidian_write"
  capabilities: "obsidian_search"
  capabilities: "obsidian_edit"
  output_types: ".md"
}

actions {
  id: "vault_setup"
  description: "Determine and resolve the Obsidian vault path"
  trigger_phrases: "set up obsidian"
  trigger_phrases: "find vault"
  trigger_phrases: "obsidian vault path"
    rules {
      text: "Resolve vault path before calling any file tools — file tools don't expand shell variables"
      priority: CRITICAL
    }
    rules {
      text: "Use OBSIDIAN_VAULT_PATH env var, fallback to ~/Documents/Obsidian Vault"
      priority: HIGH
    }
    rules {
      text: "Paths may include spaces — prefer file tools over shell commands"
      priority: HIGH
    }
    rules {
      text: "Use terminal only for resolving/verifying the vault path, then switch to file tools"
      priority: NORMAL
    }
}
actions {
  id: "read_note"
  description: "Read a note from the Obsidian vault"
  trigger_phrases: "read note"
  trigger_phrases: "open note"
  trigger_phrases: "show note content"
    rules {
      text: "Use read_file with resolved absolute path — preferable to cat (provides line numbers and pagination)"
      priority: HIGH
    }
}
actions {
  id: "list_notes"
  description: "List notes in the vault or a subfolder"
  trigger_phrases: "list notes"
  trigger_phrases: "show notes"
  trigger_phrases: "what notes exist"
    rules {
      text: "Use search_files with target='files' and pattern='*.md' under the vault path"
      priority: HIGH
    }
    rules {
      text: "To list a subfolder, search under that subfolder's absolute path"
      priority: NORMAL
    }
}
actions {
  id: "search_notes"
  description: "Search note filenames or content"
  trigger_phrases: "search notes"
  trigger_phrases: "find in notes"
  trigger_phrases: "grep vault"
    rules {
      text: "For filenames: search_files with target='files' and filename pattern"
      priority: HIGH
    }
    rules {
      text: "For content: search_files with target='content', regex pattern, file_glob='*.md'"
      priority: HIGH
    }
    rules {
      text: "Prefer search_files over grep/find/ls"
      priority: NORMAL
    }
}
actions {
  id: "create_note"
  description: "Create a new note in the vault"
  trigger_phrases: "create note"
  trigger_phrases: "new note"
  trigger_phrases: "add note to vault"
    rules {
      text: "Use write_file with resolved absolute path and full markdown content"
      priority: HIGH
    }
    rules {
      text: "Use [[Note Name]] wikilinks to connect related notes"
      priority: HIGH
    }
    rules {
      text: "Prefer write_file over shell heredocs/echo — avoids quoting issues"
      priority: NORMAL
    }
}
actions {
  id: "append_note"
  description: "Append content to an existing note"
  trigger_phrases: "append to note"
  trigger_phrases: "add to note"
  trigger_phrases: "add section to note"
    rules {
      text: "Use patch for anchored append (after a heading, before a known block)"
      priority: HIGH
    }
    rules {
      text: "For simple append with no stable context, terminal is acceptable"
      priority: HIGH
    }
    rules {
      text: "Alternative: read with read_file, then write_file to rewrite the whole note"
      priority: NORMAL
    }
}
actions {
  id: "edit_note"
  description: "Make targeted edits to an existing note"
  trigger_phrases: "edit note"
  trigger_phrases: "update note"
  trigger_phrases: "modify note"
    rules {
      text: "Use patch for focused changes when current content provides stable context"
      priority: HIGH
    }
    rules {
      text: "Prefer patch over shell text rewriting"
      priority: HIGH
    }
}

guardrails {
  text: "Always resolve vault path before calling file tools — no shell variable expansion in file tools"
  scope: ALWAYS
}

guardrails {
  text: "Use file tools (read_file, write_file, search_files, patch) over shell commands"
  scope: ALWAYS
}

guardrails {
  text: "Use [[wikilinks]] for cross-referencing notes"
  scope: WRITE_OPS
}

related {
  name: "apple-notes"
  relationship: "alternative_to"
  description: "Apple Notes for iCloud-synced notes on Apple devices"
}

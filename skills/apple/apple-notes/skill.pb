meta {
  name: "apple-notes"
  version: "1.0.0"
  summary: "Manage Apple Notes via memo CLI: create, search, edit, export"
  author: "Ion Agent"
  license: "MIT"
  platforms: "macos"
}

triggers {
  keywords: "notes"
  keywords: "apple notes"
  keywords: "memo"
  keywords: "note-taking"
  keywords: "icloud notes"
  intents: "create_note"
  intents: "search_notes"
  intents: "edit_note"
  intents: "delete_note"
  intents: "move_note"
  intents: "export_notes"
  patterns: "(create|add|save|write) .*(note|notes)"
  patterns: "(search|find|look up) .*(note|notes)"
  patterns: "(list|show|view) .*(note|notes)"
  patterns: "apple notes"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "memo"
}

provides {
  capabilities: "apple_notes_read"
  capabilities: "apple_notes_write"
  capabilities: "apple_notes_search"
  capabilities: "apple_notes_export"
  output_types: ".md"
  output_types: ".html"
}

actions {
  id: "view_notes"
  description: "List, filter, or search Apple Notes"
  trigger_phrases: "show my notes"
  trigger_phrases: "list notes"
  trigger_phrases: "search notes"
  trigger_phrases: "find notes"
    rules {
      text: "Use memo notes to list all, memo notes -f 'Folder' to filter, memo notes -s 'query' to search"
      priority: HIGH
    }
    rules {
      text: "Notes with images/attachments cannot be edited through CLI"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "list_all"
          string_value: "memo notes"
        }
        entries {
          key: "filter_folder"
          string_value: "memo notes -f 'Folder Name'"
        }
        entries {
          key: "search"
          string_value: "memo notes -s 'query'"
        }
      }
    }
}
actions {
  id: "create_notes"
  description: "Create a new Apple Note"
  trigger_phrases: "create a note"
  trigger_phrases: "add a note"
  trigger_phrases: "save to notes"
  trigger_phrases: "new note"
    rules {
      text: "Use memo notes -a 'Title' for quick add, memo notes -a for interactive editor"
      priority: HIGH
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "quick_add"
          string_value: "memo notes -a 'Note Title'"
        }
        entries {
          key: "interactive"
          string_value: "memo notes -a"
        }
      }
    }
}
actions {
  id: "edit_notes"
  description: "Edit an existing Apple Note"
  trigger_phrases: "edit a note"
  trigger_phrases: "update note"
  trigger_phrases: "modify note"
    rules {
      text: "Use memo notes -e for interactive selection to edit"
      priority: HIGH
    }
    rules {
      text: "Notes with images/attachments cannot be edited through CLI"
      priority: NORMAL
    }
}
actions {
  id: "delete_notes"
  description: "Delete an Apple Note"
  trigger_phrases: "delete a note"
  trigger_phrases: "remove note"
    rules {
      text: "Use memo notes -d for interactive selection to delete"
      priority: HIGH
    }
}
actions {
  id: "move_notes"
  description: "Move a note to a different folder"
  trigger_phrases: "move note"
  trigger_phrases: "organize notes"
    rules {
      text: "Use memo notes -m for interactive move to folder"
      priority: HIGH
    }
}
actions {
  id: "export_notes"
  description: "Export notes to HTML or Markdown"
  trigger_phrases: "export notes"
  trigger_phrases: "convert note to markdown"
    rules {
      text: "Use memo notes -ex to export to HTML/Markdown"
      priority: HIGH
    }
}

guardrails {
  text: "Notes with images or attachments cannot be edited through the CLI"
  scope: ALWAYS
}

guardrails {
  text: "Interactive prompts need terminal access (set pty=true if required)"
  scope: ALWAYS
}

guardrails {
  text: "Use memory tool for agent-internal notes, not Apple Notes"
  scope: ALWAYS
}

related {
  name: "obsidian"
  relationship: "alternative_to"
  description: "Obsidian for Markdown-native knowledge management"
}

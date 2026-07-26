meta {
  name: "apple-reminders"
  version: "1.0.0"
  summary: "Apple Reminders via remindctl: add, list, complete, manage lists"
  author: "Ion Agent"
  license: "MIT"
  platforms: "macos"
}

triggers {
  keywords: "reminders"
  keywords: "remindctl"
  keywords: "todo"
  keywords: "tasks"
  keywords: "apple reminders"
  intents: "view_reminders"
  intents: "create_reminder"
  intents: "complete_reminder"
  intents: "manage_lists"
  patterns: "(remind|reminder|reminders)"
  patterns: "(add|create|set) .*(reminder|todo|task)"
  patterns: "(list|show|view) .*(reminder|task)"
  patterns: "(complete|finish|done) .*(reminder|task)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "remindctl"
}

provides {
  capabilities: "apple_reminders_read"
  capabilities: "apple_reminders_write"
  capabilities: "apple_reminders_complete"
  output_types: ".json"
  output_types: ".txt"
}

actions {
  id: "view_reminders"
  description: "View reminders by date range or list"
  trigger_phrases: "show my reminders"
  trigger_phrases: "what's due today"
  trigger_phrases: "list reminders"
  trigger_phrases: "overdue reminders"
    rules {
      text: "Use remindctl for today, remindctl tomorrow, remindctl week, remindctl overdue, remindctl all"
      priority: HIGH
    }
    rules {
      text: "Use --json output when parsing programmatically"
      priority: HIGH
    }
    rules {
      text: "Specific date: remindctl 2026-01-04"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "today"
          string_value: "remindctl today"
        }
        entries {
          key: "tomorrow"
          string_value: "remindctl tomorrow"
        }
        entries {
          key: "week"
          string_value: "remindctl week"
        }
        entries {
          key: "overdue"
          string_value: "remindctl overdue"
        }
        entries {
          key: "all"
          string_value: "remindctl all"
        }
        entries {
          key: "specific_date"
          string_value: "remindctl YYYY-MM-DD"
        }
        entries {
          key: "json_output"
          string_value: "remindctl today --json"
        }
      }
    }
}
actions {
  id: "create_reminder"
  description: "Create a new Apple Reminder"
  trigger_phrases: "add a reminder"
  trigger_phrases: "create reminder"
  trigger_phrases: "remind me to"
  trigger_phrases: "new reminder"
    rules {
      text: "When user says 'remind me', determine if they mean Apple Reminder (synced to phone) or agent cronjob alert — ask if ambiguous"
      priority: CRITICAL
    }
    rules {
      text: "Always verify reminder details and due date with user before creating"
      priority: HIGH
    }
    rules {
      text: "Use --due for due date, --alarm for early notification time"
      priority: HIGH
    }
    rules {
      text: "Date formats: today, tomorrow, YYYY-MM-DD, YYYY-MM-DD HH:mm, ISO 8601"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "simple"
          string_value: "remindctl add \"Buy milk\""
        }
        entries {
          key: "with_options"
          string_value: "remindctl add --title \"Call mom\" --list Personal --due tomorrow"
        }
        entries {
          key: "with_datetime"
          string_value: "remindctl add --title \"Meeting prep\" --due \"2026-02-15 09:00\""
        }
        entries {
          key: "with_alarm"
          string_value: "remindctl add --title \"Hairdresser\" --due \"2026-05-15 14:00\" --alarm \"2026-05-15 13:30\""
        }
      }
    }
}
actions {
  id: "manage_lists"
  description: "Create, view, or delete reminder lists"
  trigger_phrases: "show reminder lists"
  trigger_phrases: "create reminder list"
  trigger_phrases: "delete reminder list"
    rules {
      text: "Use remindctl list to show all, remindctl list Name --create to create, remindctl list Name --delete to delete"
      priority: HIGH
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "list_all"
          string_value: "remindctl list"
        }
        entries {
          key: "show_list"
          string_value: "remindctl list Work"
        }
        entries {
          key: "create_list"
          string_value: "remindctl list Projects --create"
        }
        entries {
          key: "delete_list"
          string_value: "remindctl list Work --delete"
        }
      }
    }
}
actions {
  id: "complete_delete"
  description: "Complete or delete reminders"
  trigger_phrases: "complete reminder"
  trigger_phrases: "delete reminder"
  trigger_phrases: "mark reminder done"
    rules {
      text: "Use remindctl complete ID to complete, remindctl delete ID --force to delete"
      priority: HIGH
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "complete"
          string_value: "remindctl complete 1 2 3"
        }
        entries {
          key: "delete"
          string_value: "remindctl delete 4A83 --force"
        }
      }
    }
}

guardrails {
  text: "Always verify reminder details and due date with user before creating"
  scope: ALWAYS
}

guardrails {
  text: "Distinguish Apple Reminders from agent cronjob alerts when user says 'remind me'"
  scope: ALWAYS
}

guardrails {
  text: "Use --json output for programmatic parsing"
  scope: READ_OPS
}

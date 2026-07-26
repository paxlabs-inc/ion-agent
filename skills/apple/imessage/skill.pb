meta {
  name: "imessage"
  version: "1.0.0"
  summary: "Send and receive iMessages/SMS via the imsg CLI on macOS"
  author: "Ion Agent"
  license: "MIT"
  platforms: "macos"
}

triggers {
  keywords: "imessage"
  keywords: "imsg"
  keywords: "sms"
  keywords: "text message"
  keywords: "messages app"
  intents: "send_message"
  intents: "read_messages"
  intents: "list_chats"
  intents: "watch_messages"
  patterns: "(send|text|message) .*(imessage|sms|text)"
  patterns: "(read|show|list) .*(message|chat|conversation)"
  patterns: "imessage"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "imsg"
}

provides {
  capabilities: "imessage_send"
  capabilities: "imessage_read"
  capabilities: "imessage_watch"
  output_types: ".json"
}

actions {
  id: "list_chats"
  description: "List recent iMessage conversations"
  trigger_phrases: "list chats"
  trigger_phrases: "show conversations"
  trigger_phrases: "recent messages"
    rules {
      text: "Use imsg chats --limit N --json for structured output"
      priority: HIGH
    }
    data {
      key: "command"
      string_value: "imsg chats --limit 10 --json"
    }
}
actions {
  id: "view_history"
  description: "View message history for a chat"
  trigger_phrases: "show message history"
  trigger_phrases: "read messages"
  trigger_phrases: "conversation with"
    rules {
      text: "Use imsg history --chat-id N --limit N --json"
      priority: HIGH
    }
    rules {
      text: "Add --attachments flag to include attachment info"
      priority: NORMAL
    }
    data {
      key: "command"
      string_value: "imsg history --chat-id 1 --limit 20 --json"
    }
}
actions {
  id: "send_message"
  description: "Send an iMessage or SMS"
  trigger_phrases: "send a message"
  trigger_phrases: "text someone"
  trigger_phrases: "send imessage"
    rules {
      text: "Always verify recipient and message text before sending"
      priority: CRITICAL
    }
    rules {
      text: "Never send to unfamiliar numbers without user's explicit consent"
      priority: CRITICAL
    }
    rules {
      text: "Confirm file paths exist before including attachments"
      priority: HIGH
    }
    rules {
      text: "Use --service imessage|sms|auto to control protocol"
      priority: HIGH
    }
    rules {
      text: "Avoid spamming — throttle message rate"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "text_only"
          string_value: "imsg send --to \"+14155551212\" --text \"Hello!\""
        }
        entries {
          key: "with_attachment"
          string_value: "imsg send --to \"+14155551212\" --text \"Check this out\" --file /path/to/image.jpg"
        }
        entries {
          key: "force_imessage"
          string_value: "imsg send --to \"+14155551212\" --text \"Hi\" --service imessage"
        }
        entries {
          key: "force_sms"
          string_value: "imsg send --to \"+14155551212\" --text \"Hi\" --service sms"
        }
      }
    }
    examples {
      label: "text mom workflow"
      language: "bash"
      code: "# 1. Find mom's chat\nimsg chats --limit 20 --json | jq '.[] | select(.displayName | contains(\"Mom\"))'\n# 2. Confirm with user\n# 3. Send after confirmation\nimsg send --to \"+1555123456\" --text \"I'll be late\""
    }
}
actions {
  id: "watch_messages"
  description: "Watch for new incoming messages"
  trigger_phrases: "watch for messages"
  trigger_phrases: "monitor messages"
  trigger_phrases: "listen for messages"
    rules {
      text: "Use imsg watch --chat-id N --attachments for real-time monitoring"
      priority: HIGH
    }
    data {
      key: "command"
      string_value: "imsg watch --chat-id 1 --attachments"
    }
}

guardrails {
  text: "Always verify recipient and message text before sending anything"
  scope: WRITE_OPS
}

guardrails {
  text: "Never send to unfamiliar numbers without user's explicit consent"
  scope: WRITE_OPS
}

guardrails {
  text: "Confirm file paths exist before including attachments"
  scope: WRITE_OPS
}

meta {
  name: "himalaya"
  version: "1.1.0"
  summary: "Himalaya CLI for IMAP/SMTP email from terminal"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "email"
  keywords: "himalaya"
  keywords: "imap"
  keywords: "smtp"
  keywords: "inbox"
  keywords: "send email"
  keywords: "read email"
  intents: "send_email"
  intents: "read_email"
  intents: "list_emails"
  intents: "search_email"
  intents: "reply_email"
  intents: "manage_email"
  patterns: "(send|read|list|search|check) .*(email|mail|inbox)"
  patterns: "(reply|forward) .*(email|mail)"
  patterns: "himalaya"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "himalaya"
}

provides {
  capabilities: "email_send"
  capabilities: "email_read"
  capabilities: "email_search"
  capabilities: "email_manage"
  capabilities: "email_attachments"
  output_types: ".json"
  output_types: ".txt"
}

actions {
  id: "setup"
  description: "First-time Himalaya installation and configuration"
  trigger_phrases: "set up email"
  trigger_phrases: "configure himalaya"
  trigger_phrases: "email setup"
    rules {
      text: "Config file at ~/.config/himalaya/config.toml — run 'himalaya account configure' for interactive wizard"
      priority: CRITICAL
    }
    rules {
      text: "v1.2.0+ uses folder.aliases.X (plural dotted keys) — old [folder.alias] sub-section is silently ignored"
      priority: CRITICAL
    }
    rules {
      text: "Install: pre-built binary via curl, brew install himalaya, or cargo install himalaya"
      priority: HIGH
    }
    rules {
      text: "Store passwords securely via pass, system keyring, or a command that outputs the password"
      priority: HIGH
    }
    rules {
      text: "Use pty=true for interactive setup: terminal(command='himalaya account configure', pty=true)"
      priority: NORMAL
    }
    data {
      key: "install_methods"
      map_value {
        entries {
          key: "binary"
          string_value: "curl -sSL https://raw.githubusercontent.com/pimalaya/himalaya/master/install.sh | PREFIX=~/.local sh"
        }
        entries {
          key: "homebrew"
          string_value: "brew install himalaya"
        }
        entries {
          key: "cargo"
          string_value: "cargo install himalaya --locked"
        }
      }
    }
}
actions {
  id: "list_emails"
  description: "List emails in a folder"
  trigger_phrases: "show inbox"
  trigger_phrases: "list emails"
  trigger_phrases: "check email"
  trigger_phrases: "recent emails"
    rules {
      text: "Use --output json for structured output that's easier to parse"
      priority: HIGH
    }
    rules {
      text: "Message IDs are relative to current folder — re-list after folder changes"
      priority: HIGH
    }
    rules {
      text: "Default folder is INBOX; use --folder for others"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "inbox"
          string_value: "himalaya envelope list"
        }
        entries {
          key: "specific_folder"
          string_value: "himalaya envelope list --folder \"Sent\""
        }
        entries {
          key: "paginated"
          string_value: "himalaya envelope list --page 1 --page-size 20"
        }
        entries {
          key: "json"
          string_value: "himalaya envelope list --output json"
        }
      }
    }
}
actions {
  id: "search_emails"
  description: "Search emails by criteria"
  trigger_phrases: "search emails"
  trigger_phrases: "find email"
  trigger_phrases: "emails from"
    rules {
      text: "Use himalaya envelope list with search terms: from, subject, etc."
      priority: HIGH
    }
    data {
      key: "command"
      string_value: "himalaya envelope list from john@example.com subject meeting"
    }
}
actions {
  id: "read_email"
  description: "Read a specific email"
  trigger_phrases: "read email"
  trigger_phrases: "open email"
  trigger_phrases: "show email"
    rules {
      text: "Use himalaya message read ID for plain text, himalaya message export ID --full for raw MIME"
      priority: HIGH
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "read"
          string_value: "himalaya message read 42"
        }
        entries {
          key: "export"
          string_value: "himalaya message export 42 --full"
        }
      }
    }
}
actions {
  id: "reply_email"
  description: "Reply to an email"
  trigger_phrases: "reply to email"
  trigger_phrases: "respond to email"
  trigger_phrases: "reply"
    rules {
      text: "Non-interactive: use himalaya template reply ID | sed | himalaya template send"
      priority: HIGH
    }
    rules {
      text: "Or build reply manually with cat heredoc piped to himalaya template send"
      priority: HIGH
    }
    rules {
      text: "himalaya message reply ID --all for reply-all (needs $EDITOR)"
      priority: NORMAL
    }
    examples {
      label: "non-interactive reply"
      language: "bash"
      code: "himalaya template reply 42 | sed 's/^$/\\nYour reply text here\\n/' | himalaya template send"
    }
}
actions {
  id: "send_email"
  description: "Compose and send a new email"
  trigger_phrases: "send email"
  trigger_phrases: "compose email"
  trigger_phrases: "new email"
  trigger_phrases: "write email"
    rules {
      text: "Non-interactive from agent: pipe message via stdin to himalaya template send"
      priority: CRITICAL
    }
    rules {
      text: "Use heredoc with From/To/Subject headers followed by blank line and body"
      priority: HIGH
    }
    rules {
      text: "himalaya message write -H 'To:...' -H 'Subject:...' 'body' also works"
      priority: NORMAL
    }
    examples {
      label: "send email via heredoc"
      language: "bash"
      code: "cat << 'EOF' | himalaya template send\nFrom: you@example.com\nTo: recipient@example.com\nSubject: Test Message\n\nHello from Himalaya!\nEOF"
    }
}
actions {
  id: "manage_email"
  description: "Move, copy, delete emails and manage flags"
  trigger_phrases: "move email"
  trigger_phrases: "delete email"
  trigger_phrases: "flag email"
  trigger_phrases: "archive email"
    rules {
      text: "Move: himalaya message move 'Folder' ID (target folder first, then message ID)"
      priority: HIGH
    }
    rules {
      text: "Delete: himalaya message delete ID"
      priority: HIGH
    }
    rules {
      text: "Flags: himalaya flag add/remove ID --flag seen|answered|flagged"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "move"
          string_value: "himalaya message move \"Archive\" 42"
        }
        entries {
          key: "copy"
          string_value: "himalaya message copy \"Important\" 42"
        }
        entries {
          key: "delete"
          string_value: "himalaya message delete 42"
        }
        entries {
          key: "add_flag"
          string_value: "himalaya flag add 42 --flag seen"
        }
        entries {
          key: "remove_flag"
          string_value: "himalaya flag remove 42 --flag seen"
        }
      }
    }
}
actions {
  id: "manage_folders"
  description: "List and manage email folders"
  trigger_phrases: "list folders"
  trigger_phrases: "show folders"
  trigger_phrases: "email folders"
    rules {
      text: "Use himalaya folder list to show all folders"
      priority: HIGH
    }
    data {
      key: "command"
      string_value: "himalaya folder list"
    }
}

guardrails {
  text: "Always use folder.aliases.X (plural dotted keys) in config — old alias sub-section is silently ignored on v1.2.0+"
  scope: ALWAYS
}

guardrails {
  text: "Message IDs are relative to current folder — re-list after folder changes"
  scope: READ_OPS
}

guardrails {
  text: "Use --output json for structured programmatic output"
  scope: READ_OPS
}

guardrails {
  text: "Pipe input for composing emails — avoid interactive $EDITOR when possible from agent"
  scope: WRITE_OPS
}

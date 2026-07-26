meta {
  name: "teams-meeting-pipeline"
  version: "1.1.0"
  summary: "Operate the Teams meeting summary pipeline — summarize, inspect, replay, manage Graph subscriptions"
  author: "Ion Agent + Teknium"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "teams"
  keywords: "teams meeting"
  keywords: "meeting summary"
  keywords: "meeting transcript"
  keywords: "teams pipeline"
  keywords: "microsoft graph"
  keywords: "graph subscription"
  keywords: "teams webhook"
  keywords: "action items"
  intents: "teams_summarize"
  intents: "teams_pipeline_status"
  intents: "teams_replay_job"
  intents: "teams_manage_subscriptions"
  intents: "teams_validate_setup"
  patterns: "(summarize|summary|transcript|notes|action items) .*(teams|meeting)"
  patterns: "(teams|meeting) .*(summary|transcript|pipeline|status)"
  patterns: "(replay|re-run|rerun) .*(job|meeting|summary)"
  patterns: "(graph|webhook) .*(subscription|subscribe|renew)"
  patterns: "pipeline .*(status|validate|list|show)"
  anti_patterns: "zoom meeting"
  anti_patterns: "google meet"
}

requires {
  env_all: "MSGRAPH_TENANT_ID"
  env_all: "MSGRAPH_CLIENT_ID"
  env_all: "MSGRAPH_CLIENT_SECRET"
  tools {
    name: "terminal"
    required: true
  }
  binaries: "ion"
}

provides {
  capabilities: "teams_meeting_summary"
  capabilities: "teams_pipeline_management"
  capabilities: "graph_subscription_management"
  capabilities: "teams_transcript_extraction"
  output_types: ".json"
}

actions {
  id: "validate_setup"
  description: "Validate Microsoft Graph and pipeline configuration"
  trigger_phrases: "validate setup"
  trigger_phrases: "check teams config"
  trigger_phrases: "is teams pipeline working"
  trigger_phrases: "test graph setup"
    rules {
      text: "Run validate, token-health, then subscriptions — if all three pass, request a test meeting"
      priority: CRITICAL
    }
    rules {
      text: "If env vars missing, direct user to Azure app registration guide at /docs/guides/microsoft-graph-app-registration"
      priority: HIGH
    }
    data {
      key: "validation_commands"
      list_value {
        items {
          string_value: "ion teams-pipeline validate"
        }
        items {
          string_value: "ion teams-pipeline token-health"
        }
        items {
          string_value: "ion teams-pipeline subscriptions"
        }
      }
    }
}
actions {
  id: "list_jobs"
  description: "List recent meeting jobs in the pipeline"
  trigger_phrases: "list meetings"
  trigger_phrases: "recent meetings"
  trigger_phrases: "pipeline status"
  trigger_phrases: "show jobs"
  trigger_phrases: "meeting history"
    rules {
      text: "ion teams-pipeline list — add --status failed to filter failures"
      priority: HIGH
    }
    rules {
      text: "For 'why no summary': start with list --status failed, then show <job-id>"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "ion teams-pipeline list [--status failed|completed|pending]"
    }
    examples {
      label: "list recent jobs"
      language: "bash"
      code: "ion teams-pipeline list"
    }
    examples {
      label: "list only failed jobs"
      language: "bash"
      code: "ion teams-pipeline list --status failed"
    }
}
actions {
  id: "show_job"
  description: "Show full detail of a specific meeting job"
  trigger_phrases: "show job"
  trigger_phrases: "job details"
  trigger_phrases: "meeting details"
  trigger_phrases: "inspect job"
    rules {
      text: "ion teams-pipeline show <job-id> — shows full job detail including errors"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "ion teams-pipeline show JOB_ID"
    }
}
actions {
  id: "replay_job"
  description: "Re-run a stored meeting job for fresh summary"
  trigger_phrases: "replay job"
  trigger_phrases: "re-run summary"
  trigger_phrases: "reprocess meeting"
  trigger_phrases: "try again"
    rules {
      text: "ion teams-pipeline run <job-id> — re-summarizes and re-delivers"
      priority: HIGH
    }
    rules {
      text: "If replay fails: show <job-id> to inspect error, fetch --meeting-id to dry-run artifact resolution"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "ion teams-pipeline run JOB_ID"
    }
}
actions {
  id: "fetch_dry_run"
  description: "Dry-run: resolve meeting and transcript without persisting"
  trigger_phrases: "fetch meeting"
  trigger_phrases: "dry run"
  trigger_phrases: "check transcript"
  trigger_phrases: "resolve meeting"
    rules {
      text: "ion teams-pipeline fetch --meeting-id <id> or --join-web-url \"<url>\""
      priority: HIGH
    }
    rules {
      text: "Transcript may not be available immediately — Teams takes 2-5 min after meeting ends"
      priority: NORMAL
    }
    data {
      key: "command_patterns"
      map_value {
        entries {
          key: "by_meeting_id"
          string_value: "ion teams-pipeline fetch --meeting-id MEETING_ID"
        }
        entries {
          key: "by_join_url"
          string_value: "ion teams-pipeline fetch --join-web-url \"URL\""
        }
      }
    }
}
actions {
  id: "manage_subscriptions"
  description: "Create, renew, delete, or inspect Graph webhook subscriptions"
  trigger_phrases: "manage subscription"
  trigger_phrases: "create webhook"
  trigger_phrases: "renew subscription"
  trigger_phrases: "delete subscription"
  trigger_phrases: "graph subscriptions"
    rules {
      text: "Graph subscriptions expire in 72 hours and will NOT auto-renew — must schedule maintain-subscriptions"
      priority: CRITICAL
    }
    rules {
      text: "Without automated renewal, meeting notifications silently stop arriving after 3 days"
      priority: CRITICAL
    }
    rules {
      text: "12-hour renewal interval is safe (6x headroom against 72h limit)"
      priority: HIGH
    }
    rules {
      text: "Set up automated renewal via ion cron add, systemd timer, or crontab"
      priority: HIGH
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "list"
          string_value: "ion teams-pipeline subscriptions"
        }
        entries {
          key: "create"
          string_value: "ion teams-pipeline subscribe --resource RESOURCE --notification-url URL --client-state STATE"
        }
        entries {
          key: "renew"
          string_value: "ion teams-pipeline renew-subscription SUB_ID --expiration ISO_DATE"
        }
        entries {
          key: "delete"
          string_value: "ion teams-pipeline delete-subscription SUB_ID"
        }
        entries {
          key: "maintain"
          string_value: "ion teams-pipeline maintain-subscriptions [--dry-run]"
        }
      }
    }
    data {
      key: "subscription_expiry_hours"
      int_value: 72
    }
    data {
      key: "recommended_renewal_hours"
      int_value: 12
    }
}
actions {
  id: "troubleshoot"
  description: "Troubleshoot pipeline issues — missing summaries, ingestion failures"
  trigger_phrases: "meeting summary not arrived"
  trigger_phrases: "no new meetings"
  trigger_phrases: "pipeline broken"
  trigger_phrases: "troubleshoot teams"
  trigger_phrases: "why no summary"
    rules {
      text: "Most common cause: expired Graph subscriptions — check subscriptions first"
      priority: CRITICAL
    }
    rules {
      text: "Decision tree: list --status failed → show <job-id> → if no job exists → check subscriptions"
      priority: HIGH
    }
    rules {
      text: "If validate+token-health+subscriptions all pass but no ingestion → check webhook URL and delivery mode config"
      priority: HIGH
    }
    rules {
      text: "Transcript not available yet? Wait 2-5 min after meeting ends and retry"
      priority: NORMAL
    }
    rules {
      text: "401/403 on Graph calls despite valid token → admin consent not re-granted after permission changes"
      priority: NORMAL
    }
}

guardrails {
  text: "Graph subscriptions expire in 72h — always set up automated renewal"
  scope: ALWAYS
}

guardrails {
  text: "Verify env vars (MSGRAPH_TENANT_ID, MSGRAPH_CLIENT_ID, MSGRAPH_CLIENT_SECRET) before first use"
  scope: ALWAYS
}

guardrails {
  text: "Transcript availability has 2-5 min delay after meeting ends — don't fail immediately on empty"
  scope: READ_OPS
}

guardrails {
  text: "Point users to full docs: /docs/guides/operate-teams-meeting-pipeline for runbook"
  scope: ALWAYS
}

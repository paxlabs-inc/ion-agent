---
name: apple-reminders
description: "Apple Reminders via remindctl: add, list, complete."
version: 1.0.0
author: Ion Agent
license: MIT
platforms: [macos]
metadata:
  ion:
    tags: [Reminders, tasks, todo, macOS, Apple]
prerequisites:
  commands: [remindctl]
---

# Apple Reminders

The `remindctl` command gives you terminal-based access to Apple Reminders. Tasks you create sync across all Apple devices through iCloud.

## Prerequisites

- **macOS** with Reminders.app installed
- Install via Homebrew: `brew install steipete/tap/remindctl`
- Approve Reminders access when the system prompts you
- Verify access with: `remindctl status` / Request it with: `remindctl authorize`

## When to Use

- The user mentions "reminder" or the Reminders app
- Setting up personal to-dos with deadlines that appear on iOS
- Working with Apple Reminders lists
- The user wants tasks visible on their iPhone or iPad

## When NOT to Use

- Scheduling alerts for the agent → use the cronjob tool instead
- Creating calendar events → use Apple Calendar or Google Calendar
- Managing project tasks → use GitHub Issues, Notion, or similar tools
- When the user says "remind me" but actually means an agent alert → ask for clarification first

## Quick Reference

### View Reminders

```bash
remindctl                    # Today's reminders
remindctl today              # Today
remindctl tomorrow           # Tomorrow
remindctl week               # This week
remindctl overdue            # Past due
remindctl all                # Everything
remindctl 2026-01-04         # Specific date
```

### Manage Lists

```bash
remindctl list               # List all lists
remindctl list Work          # Show specific list
remindctl list Projects --create    # Create list
remindctl list Work --delete        # Delete list
```

### Create Reminders

```bash
remindctl add "Buy milk"
remindctl add --title "Call mom" --list Personal --due tomorrow
remindctl add --title "Meeting prep" --due "2026-02-15 09:00"
```

### Due Time vs Alarm / Early Nudge

The `--due` and `--alarm` flags control different fields:

- `--due` determines the reminder's due date and time.
- `--alarm` determines when the EventKit alarm/notification fires. Reminders with a timed due date may default to an alarm at the due time, but you should pass `--alarm` explicitly when the user requests an earlier notification.

Example: a reminder due at 2:00 PM with a 30-minute-early notification:

```bash
remindctl add --title "Hairdresser" --due "2026-05-15 14:00" --alarm "2026-05-15 13:30"
```

Updating an existing reminder:

```bash
remindctl edit 87354 --due "2026-05-15 14:00" --alarm "2026-05-15 13:30"
```

The Reminders UI might display or group the item by its alarm time, since that's when the notification triggers. To confirm the actual due time hasn't shifted, check the JSON output rather than relying on the UI:

```bash
remindctl today --json
```

The relevant fields in the output:

- `dueDate`: the actual due time
- `alarmDate`: the notification / early nudge time

Apple's public `EKReminder` documentation only lists reminder-specific properties. Alarm functionality comes from the inherited `EKCalendarItem` behavior that remindctl exposes through its `--alarm` flag.

### Complete / Delete

```bash
remindctl complete 1 2 3          # Complete by ID
remindctl delete 4A83 --force     # Delete by ID
```

### Output Formats

```bash
remindctl today --json       # JSON for scripting
remindctl today --plain      # TSV format
remindctl today --quiet      # Counts only
```

## Date Formats

The following formats are accepted by `--due` and date-based filters:
- `today`, `tomorrow`, `yesterday`
- `YYYY-MM-DD`
- `YYYY-MM-DD HH:mm`
- ISO 8601 (`2026-01-04T12:34:56Z`)

## Rules

1. When the user says "remind me", determine whether they mean an Apple Reminder (synced to their phone) or an agent cronjob alert — ask if it's ambiguous
2. Always verify the reminder details and due date with the user before creating it
3. Rely on `--json` output when parsing programmatically

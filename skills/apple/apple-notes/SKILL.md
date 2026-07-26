---
name: apple-notes
description: "Manage Apple Notes via memo CLI: create, search, edit."
version: 1.0.0
author: Ion Agent
license: MIT
platforms: [macos]
metadata:
  ion:
    tags: [Notes, Apple, macOS, note-taking]
    related_skills: [obsidian]
prerequisites:
  commands: [memo]
---

# Apple Notes

The `memo` CLI lets you handle Apple Notes straight from the terminal. Any changes sync automatically to all your Apple devices through iCloud.

## Prerequisites

- **macOS** with Notes.app installed
- Install via Homebrew: `brew tap antoniorodr/memo && brew install antoniorodr/memo/memo`
- When prompted, grant Notes.app Automation access (System Settings → Privacy → Automation)

## When to Use

- The user wants to create, view, or search their Apple Notes
- Saving content to Notes.app so it's available on every device
- Sorting notes into folders
- Converting notes to Markdown or HTML format

## When NOT to Use

- Working with an Obsidian vault → switch to the `obsidian` skill
- Bear Notes interactions → that's a different app and not covered here
- Notes only the agent needs internally → prefer the `memory` tool

## Quick Reference

### View Notes

```bash
memo notes                        # List all notes
memo notes -f "Folder Name"       # Filter by folder
memo notes -s "query"             # Search notes (fuzzy)
```

### Create Notes

```bash
memo notes -a                     # Interactive editor
memo notes -a "Note Title"        # Quick add with title
```

### Edit Notes

```bash
memo notes -e                     # Interactive selection to edit
```

### Delete Notes

```bash
memo notes -d                     # Interactive selection to delete
```

### Move Notes

```bash
memo notes -m                     # Move note to folder (interactive)
```

### Export Notes

```bash
memo notes -ex                    # Export to HTML/Markdown
```

## Limitations

- Notes that contain images or attachments cannot be edited through the CLI
- Interactive prompts need terminal access (set pty=true if required)
- macOS exclusive — depends on the Apple Notes.app being present

## Rules

1. Reach for Apple Notes when the user needs cross-device syncing (iPhone/iPad/Mac)
2. Reserve the `memory` tool for agent-internal notes that don't need to be synced
3. Turn to the `obsidian` skill when Markdown-native knowledge management is the goal

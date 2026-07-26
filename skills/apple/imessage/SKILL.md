---
name: imessage
description: Send and receive iMessages/SMS via the imsg CLI on macOS.
version: 1.0.0
author: Ion Agent
license: MIT
platforms: [macos]
metadata:
  ion:
    tags: [iMessage, SMS, messaging, macOS, Apple]
prerequisites:
  commands: [imsg]
---

# iMessage

The `imsg` CLI enables reading and sending iMessage/SMS through macOS Messages.app from the terminal.

## Prerequisites

- **macOS** with Messages.app signed in
- Install via Homebrew: `brew install steipete/tap/imsg`
- Grant Full Disk Access to the terminal (System Settings → Privacy → Full Disk Access)
- Approve Automation permission for Messages.app when prompted

## When to Use

- The user wants to send an iMessage or SMS text
- Retrieving iMessage conversation history
- Reviewing recent Messages.app conversations
- Sending messages to phone numbers or Apple IDs

## When NOT to Use

- Telegram, Discord, Slack, or WhatsApp messages → use the relevant gateway channel
- Managing group chats (adding or removing participants) → not supported
- Bulk or mass messaging → always get explicit confirmation from the user first

## Quick Reference

### List Chats

```bash
imsg chats --limit 10 --json
```

### View History

```bash
# By chat ID
imsg history --chat-id 1 --limit 20 --json

# With attachments info
imsg history --chat-id 1 --limit 20 --attachments --json
```

### Send Messages

```bash
# Text only
imsg send --to "+14155551212" --text "Hello!"

# With attachment
imsg send --to "+14155551212" --text "Check this out" --file /path/to/image.jpg

# Force iMessage or SMS
imsg send --to "+14155551212" --text "Hi" --service imessage
imsg send --to "+14155551212" --text "Hi" --service sms
```

### Watch for New Messages

```bash
imsg watch --chat-id 1 --attachments
```

## Service Options

- `--service imessage` — Send via iMessage only (recipient must have iMessage)
- `--service sms` — Send via SMS only (green bubble)
- `--service auto` — Let Messages.app choose the protocol (default)

## Rules

1. **Always verify the recipient and message text** before sending anything
2. **Never send to unfamiliar numbers** without the user's explicit consent
3. **Confirm file paths exist** before including attachments
4. **Avoid spamming** — throttle your message rate

## Example Workflow

User: "Text mom that I'll be late"

```bash
# 1. Find mom's chat
imsg chats --limit 20 --json | jq '.[] | select(.displayName | contains("Mom"))'

# 2. Confirm with user: "Found Mom at +1555123456. Send 'I'll be late' via iMessage?"

# 3. Send after confirmation
imsg send --to "+1555123456" --text "I'll be late"
```

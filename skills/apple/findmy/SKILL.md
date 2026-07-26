---
name: findmy
description: "Track Apple devices/AirTags via FindMy.app on macOS."
version: 1.0.0
author: Ion Agent
license: MIT
platforms: [macos]
metadata:
  ion:
    tags: [FindMy, AirTag, location, tracking, macOS, Apple]
---

# Find My (Apple)

Locate Apple devices and AirTags through the FindMy.app on macOS. Apple provides no CLI for FindMy, so this skill relies on AppleScript to launch the app and screen capture to read where devices are.

## Prerequisites

- **macOS** with the Find My app installed and signed into iCloud
- Devices or AirTags already linked to Find My
- Screen Recording permission granted to the terminal (System Settings → Privacy → Screen Recording)
- **Optional but recommended**: Install `peekaboo` for enhanced UI automation:
  `brew install steipete/tap/peekaboo`

## When to Use

- The user asks where a specific device, pet, keys, or bag is
- Looking up AirTag positions
- Verifying the location of iPhones, iPads, Macs, or AirPods
- Tracking movement of a pet or item over time (AirTag patrol routes)

## Method 1: AppleScript + Screenshot (Basic)

### Open FindMy and Navigate

```bash
# Open Find My app
osascript -e 'tell application "FindMy" to activate'

# Wait for it to load
sleep 3

# Take a screenshot of the Find My window
screencapture -w -o /tmp/findmy.png
```

Then pass the screenshot to `vision_analyze` for reading:
```
vision_analyze(image_url="/tmp/findmy.png", question="What devices/items are shown and what are their locations?")
```

### Switch Between Tabs

```bash
# Switch to Devices tab
osascript -e '
tell application "System Events"
    tell process "FindMy"
        click button "Devices" of toolbar 1 of window 1
    end tell
end tell'

# Switch to Items tab (AirTags)
osascript -e '
tell application "System Events"
    tell process "FindMy"
        click button "Items" of toolbar 1 of window 1
    end tell
end tell'
```

## Method 2: Peekaboo UI Automation (Recommended)

When `peekaboo` is available, it provides more dependable UI interaction:

```bash
# Open Find My
osascript -e 'tell application "FindMy" to activate'
sleep 3

# Capture and annotate the UI
peekaboo see --app "FindMy" --annotate --path /tmp/findmy-ui.png

# Click on a specific device/item by element ID
peekaboo click --on B3 --app "FindMy"

# Capture the detail view
peekaboo image --app "FindMy" --path /tmp/findmy-detail.png
```

Then analyze with vision:
```
vision_analyze(image_url="/tmp/findmy-detail.png", question="What is the location shown for this device/item? Include address and coordinates if visible.")
```

## Workflow: Track AirTag Location Over Time

To monitor an AirTag over a period (e.g., following a cat's patrol route):

```bash
# 1. Open FindMy to Items tab
osascript -e 'tell application "FindMy" to activate'
sleep 3

# 2. Click on the AirTag item (stay on page — AirTag only updates when page is open)

# 3. Periodically capture location
while true; do
    screencapture -w -o /tmp/findmy-$(date +%H%M%S).png
    sleep 300  # Every 5 minutes
done
```

Run each screenshot through vision to pull coordinates, then assemble the route.

## Limitations

- FindMy offers **no CLI or API** — UI automation is the only option
- AirTag locations only refresh while the FindMy page is actively in view
- Accuracy depends on how many nearby Apple devices are in the FindMy network
- Screen Recording permission is needed for screenshots
- AppleScript-based UI automation may break across different macOS versions

## Rules

1. Keep the FindMy app in the foreground while tracking AirTags (location updates stop when the window is minimized)
2. Use `vision_analyze` to interpret screenshot content — don't attempt to parse raw pixels
3. For continuous tracking, set up a cronjob that captures and logs locations at intervals
4. Honor privacy — only track devices and items that belong to the user

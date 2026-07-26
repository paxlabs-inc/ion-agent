meta {
  name: "findmy"
  version: "1.0.0"
  summary: "Track Apple devices/AirTags via FindMy.app on macOS"
  author: "Ion Agent"
  license: "MIT"
  platforms: "macos"
}

triggers {
  keywords: "find my"
  keywords: "findmy"
  keywords: "airtag"
  keywords: "device tracking"
  keywords: "location"
  keywords: "apple devices"
  intents: "find_device"
  intents: "track_airtag"
  intents: "locate_device"
  patterns: "(where is|find|locate) .*(device|phone|ipad|mac|airpod|airtag|keys|bag)"
  patterns: "(track|tracking) .*(device|airtag|location)"
  patterns: "find my"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
}

provides {
  capabilities: "device_location"
  capabilities: "airtag_tracking"
  output_types: ".png"
}

actions {
  id: "find_device"
  description: "Locate an Apple device or AirTag via FindMy.app"
  trigger_phrases: "where is my phone"
  trigger_phrases: "find my ipad"
  trigger_phrases: "locate my airpods"
  trigger_phrases: "where are my keys"
    rules {
      text: "FindMy has no CLI or API — UI automation via AppleScript/screenshots is the only option"
      priority: CRITICAL
    }
    rules {
      text: "Keep FindMy app in foreground while tracking AirTags (location updates stop when minimized)"
      priority: CRITICAL
    }
    rules {
      text: "Use vision_analyze to interpret screenshots — don't attempt to parse raw pixels"
      priority: HIGH
    }
    rules {
      text: "Screen Recording permission required for screenshots"
      priority: HIGH
    }
    rules {
      text: "Install peekaboo for enhanced UI automation: brew install steipete/tap/peekaboo"
      priority: NORMAL
    }
    data {
      key: "method_basic"
      list_value {
        items {
          string_value: "osascript -e 'tell application \"FindMy\" to activate'"
        }
        items {
          string_value: "sleep 3"
        }
        items {
          string_value: "screencapture -w -o /tmp/findmy.png"
        }
      }
    }
    data {
      key: "method_peekaboo"
      list_value {
        items {
          string_value: "osascript -e 'tell application \"FindMy\" to activate'"
        }
        items {
          string_value: "sleep 3"
        }
        items {
          string_value: "peekaboo see --app \"FindMy\" --annotate --path /tmp/findmy-ui.png"
        }
        items {
          string_value: "peekaboo click --on B3 --app FindMy"
        }
      }
    }
    examples {
      label: "open FindMy and capture screenshot"
      language: "bash"
      code: "osascript -e 'tell application \"FindMy\" to activate'\nsleep 3\nscreencapture -w -o /tmp/findmy.png"
    }
}
actions {
  id: "switch_tabs"
  description: "Switch between Devices and Items tabs in FindMy"
  trigger_phrases: "show devices"
  trigger_phrases: "show airtags"
  trigger_phrases: "switch to items tab"
    rules {
      text: "Use AppleScript to click toolbar buttons for tab switching"
      priority: HIGH
    }
    examples {
      label: "switch to Items tab"
      language: "bash"
      code: "osascript -e '\ntell application \"System Events\"\n    tell process \"FindMy\"\n        click button \"Items\" of toolbar 1 of window 1\n    end tell\nend tell'"
    }
}
actions {
  id: "track_airtag"
  description: "Monitor an AirTag location over time"
  trigger_phrases: "track my airtag"
  trigger_phrases: "follow airtag"
  trigger_phrases: "monitor airtag location"
    rules {
      text: "AirTag locations only refresh while FindMy page is actively in view"
      priority: CRITICAL
    }
    rules {
      text: "Set up cronjob for periodic screenshot capture and vision analysis"
      priority: HIGH
    }
    rules {
      text: "Only track devices/items that belong to the user — honor privacy"
      priority: HIGH
    }
    examples {
      label: "periodic airtag capture loop"
      language: "bash"
      code: "while true; do\n    screencapture -w -o /tmp/findmy-$(date +%H%M%S).png\n    sleep 300\ndone"
    }
}

guardrails {
  text: "Only track devices and items that belong to the user — honor privacy"
  scope: ALWAYS
}

guardrails {
  text: "Never close or switch away from FindMy while actively tracking AirTags"
  scope: ALWAYS
}

guardrails {
  text: "Use vision_analyze to interpret screenshots, not raw pixel parsing"
  scope: ALWAYS
}

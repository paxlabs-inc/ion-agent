meta {
  name: "petdex"
  version: "1.0.0"
  summary: "Install and select animated petdex mascots for Ion"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "petdex"
  keywords: "pet"
  keywords: "mascot"
  keywords: "desktop pet"
  keywords: "terminal pet"
  keywords: "ion pets"
  intents: "pet_install"
  intents: "pet_select"
  intents: "pet_preview"
  intents: "pet_disable"
  intents: "pet_diagnose"
  patterns: "(install|select|choose|preview|show|disable|remove) .*(pet|mascot)"
  patterns: "petdex"
  patterns: "ion pets"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "python3"
}

provides {
  capabilities: "pet_management"
  capabilities: "pet_preview"
  capabilities: "pet_diagnostics"
}

actions {
  id: "list_pets"
  description: "Browse available pets in the gallery"
  trigger_phrases: "list pets"
  trigger_phrases: "show pets"
  trigger_phrases: "browse pets"
  trigger_phrases: "available pets"
  trigger_phrases: "petdex list"
    rules {
      text: "ion pets list to browse gallery — add substring to filter: ion pets list cat"
      priority: HIGH
    }
    rules {
      text: "ion pets list --installed shows only installed pets"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "ion pets list [FILTER]"
    }
    examples {
      label: "list all pets"
      language: "bash"
      code: "ion pets list"
    }
    examples {
      label: "filter by keyword"
      language: "bash"
      code: "ion pets list cat"
    }
}
actions {
  id: "install_pet"
  description: "Install and optionally activate a pet"
  trigger_phrases: "install pet"
  trigger_phrases: "get pet"
  trigger_phrases: "add pet"
    rules {
      text: "ion pets install <slug> --select to install and activate in one step"
      priority: HIGH
    }
    rules {
      text: "Pets install into <ION_HOME>/pets/<slug>/ (profile-aware)"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "ion pets install SLUG [--select]"
    }
    examples {
      label: "install and activate"
      language: "bash"
      code: "ion pets install <slug> --select"
    }
}
actions {
  id: "select_pet"
  description: "Set the active pet or open picker"
  trigger_phrases: "select pet"
  trigger_phrases: "change pet"
  trigger_phrases: "switch pet"
  trigger_phrases: "activate pet"
    rules {
      text: "ion pets select <slug> — omit slug for interactive picker"
      priority: HIGH
    }
    rules {
      text: "Writes display.pet.slug + display.pet.enabled to config.yaml"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "ion pets select [SLUG]"
    }
}
actions {
  id: "preview_pet"
  description: "Preview or animate a pet in terminal"
  trigger_phrases: "preview pet"
  trigger_phrases: "show pet"
  trigger_phrases: "animate pet"
    rules {
      text: "ion pets show [slug] [--cycle] [--state run] — Ctrl+C to stop"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "ion pets show [SLUG] [--cycle] [--state STATE]"
    }
}
actions {
  id: "scale_pet"
  description: "Resize the pet display"
  trigger_phrases: "resize pet"
  trigger_phrases: "scale pet"
  trigger_phrases: "make pet bigger"
  trigger_phrases: "make pet smaller"
    rules {
      text: "ion pets scale <factor> — clamped 0.1–3.0, one knob resizes every surface"
      priority: HIGH
    }
    data {
      key: "command_pattern"
      string_value: "ion pets scale FACTOR"
    }
    data {
      key: "scale_range"
      map_value {
        entries {
          key: "min"
          float_value: 0.1
        }
        entries {
          key: "max"
          float_value: 3.0
        }
        entries {
          key: "default"
          float_value: 0.33
        }
      }
    }
}
actions {
  id: "disable_pet"
  description: "Disable or remove the active pet"
  trigger_phrases: "disable pet"
  trigger_phrases: "turn off pet"
  trigger_phrases: "remove pet"
  trigger_phrases: "hide pet"
    rules {
      text: "ion pets off to disable, ion pets remove <slug> to uninstall"
      priority: HIGH
    }
    data {
      key: "command_patterns"
      map_value {
        entries {
          key: "disable"
          string_value: "ion pets off"
        }
        entries {
          key: "remove"
          string_value: "ion pets remove SLUG"
        }
      }
    }
}
actions {
  id: "diagnose"
  description: "Diagnose pet setup issues"
  trigger_phrases: "pet not showing"
  trigger_phrases: "pet doctor"
  trigger_phrases: "diagnose pet"
  trigger_phrases: "pet not working"
    rules {
      text: "ion pets doctor shows: resolved pet, render mode, terminal graphics protocol, effective mode"
      priority: HIGH
    }
    rules {
      text: "A pet only shows once installed AND selected (enabled: true)"
      priority: NORMAL
    }
    rules {
      text: "Terminal rendering disabled in pipes/redirects (no TTY) — by design"
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "ion pets doctor"
    }
    data {
      key: "render_modes"
      list_value {
        items {
          string_value: "auto"
        }
        items {
          string_value: "kitty"
        }
        items {
          string_value: "iterm"
        }
        items {
          string_value: "sixel"
        }
        items {
          string_value: "unicode"
        }
        items {
          string_value: "off"
        }
      }
    }
}

guardrails {
  text: "Network access to petdex.dev required for gallery/manifest (read-only, no auth)"
  scope: ALWAYS
}

guardrails {
  text: "Graphics-capable terminal needed for full-fidelity: kitty, Ghostty, WezTerm, iTerm2, or sixel"
  scope: ALWAYS
}

guardrails {
  text: "Ion uses <ION_HOME>/pets/ (not ~/.codex/pets/) — install through ion pets"
  scope: ALWAYS
}

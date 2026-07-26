meta {
  name: "openhue"
  version: "1.0.0"
  summary: "Control Philips Hue lights, scenes, rooms via OpenHue CLI"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "hue"
  keywords: "lights"
  keywords: "philips hue"
  keywords: "openhue"
  keywords: "smart lights"
  keywords: "smart home"
  intents: "control_lights"
  intents: "set_scene"
  intents: "dim_lights"
  intents: "change_color"
  intents: "control_rooms"
  patterns: "(turn|switch) .*(light|lights|lamp|lamps) .*(on|off)"
  patterns: "(dim|brighten|set) .*(light|lights|room)"
  patterns: "(set|activate) .*(scene|movie mode|relax|concentrate)"
  patterns: "(change|set) .*(color|colour|temperature) .*(light|lights)"
  patterns: "hue"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "openhue"
}

provides {
  capabilities: "hue_light_control"
  capabilities: "hue_room_control"
  capabilities: "hue_scene_control"
}

actions {
  id: "list_resources"
  description: "List available Hue lights, rooms, and scenes"
  trigger_phrases: "list lights"
  trigger_phrases: "show rooms"
  trigger_phrases: "what scenes"
  trigger_phrases: "hue devices"
    rules {
      text: "Use openhue get light/room/scene to list resources"
      priority: HIGH
    }
    rules {
      text: "Light and room names are case-sensitive — verify exact names before setting"
      priority: HIGH
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "lights"
          string_value: "openhue get light"
        }
        entries {
          key: "rooms"
          string_value: "openhue get room"
        }
        entries {
          key: "scenes"
          string_value: "openhue get scene"
        }
      }
    }
}
actions {
  id: "control_lights"
  description: "Turn on/off, dim, or change color of individual lights"
  trigger_phrases: "turn on light"
  trigger_phrases: "turn off light"
  trigger_phrases: "dim light"
  trigger_phrases: "set light color"
    rules {
      text: "Use openhue set light 'Name' --on/--off --brightness N --temperature N --color NAME"
      priority: HIGH
    }
    rules {
      text: "Color settings only apply to color-capable bulbs (white-only models ignore them)"
      priority: HIGH
    }
    rules {
      text: "Brightness: 0-100, Temperature: 153-500 mirek (warm to cool), Color: name or hex"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "True"
          string_value: "openhue set light \"Bedroom Lamp\" --on"
        }
        entries {
          key: "False"
          string_value: "openhue set light \"Bedroom Lamp\" --off"
        }
        entries {
          key: "brightness"
          string_value: "openhue set light \"Bedroom Lamp\" --on --brightness 50"
        }
        entries {
          key: "temperature"
          string_value: "openhue set light \"Bedroom Lamp\" --on --temperature 300"
        }
        entries {
          key: "color_name"
          string_value: "openhue set light \"Bedroom Lamp\" --on --color red"
        }
        entries {
          key: "color_hex"
          string_value: "openhue set light \"Bedroom Lamp\" --on --rgb \"#FF5500\""
        }
      }
    }
}
actions {
  id: "control_rooms"
  description: "Control all lights in a room"
  trigger_phrases: "turn off room"
  trigger_phrases: "dim room"
  trigger_phrases: "room lights"
    rules {
      text: "Use openhue set room 'Name' --on/--off --brightness N"
      priority: HIGH
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "False"
          string_value: "openhue set room \"Bedroom\" --off"
        }
        entries {
          key: "brightness"
          string_value: "openhue set room \"Bedroom\" --on --brightness 30"
        }
      }
    }
}
actions {
  id: "set_scene"
  description: "Activate a Hue scene in a room"
  trigger_phrases: "set scene"
  trigger_phrases: "movie mode"
  trigger_phrases: "relax mode"
  trigger_phrases: "activate scene"
    rules {
      text: "Use openhue set scene 'Scene Name' --room 'Room Name'"
      priority: HIGH
    }
    rules {
      text: "Quick presets: bedtime=20%+450mirek, work=100%+250mirek, movie=10%"
      priority: NORMAL
    }
    data {
      key: "commands"
      map_value {
        entries {
          key: "set_scene"
          string_value: "openhue set scene \"Relax\" --room \"Bedroom\""
        }
        entries {
          key: "bedtime"
          string_value: "openhue set room \"Bedroom\" --on --brightness 20 --temperature 450"
        }
        entries {
          key: "work"
          string_value: "openhue set room \"Office\" --on --brightness 100 --temperature 250"
        }
        entries {
          key: "movie"
          string_value: "openhue set room \"Living Room\" --on --brightness 10"
        }
        entries {
          key: "all_off"
          string_value: "openhue set room \"Bedroom\" --off && openhue set room \"Office\" --off"
        }
      }
    }
}

guardrails {
  text: "Bridge must be on the same local network as the machine"
  scope: ALWAYS
}

guardrails {
  text: "Light and room names are case-sensitive — verify exact names with get commands first"
  scope: ALWAYS
}

guardrails {
  text: "Initial setup requires physically pressing the button on the Hue Bridge"
  scope: AUTH_OPS
}

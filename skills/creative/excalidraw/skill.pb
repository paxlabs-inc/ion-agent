meta {
  name: "excalidraw"
  version: "1.0.0"
  summary: "Hand-drawn Excalidraw JSON diagrams (arch, flow, seq)"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "excalidraw"
  keywords: "diagram"
  keywords: "flowchart"
  keywords: "architecture diagram"
  keywords: "sequence diagram"
  intents: "create_diagram"
  intents: "draw_flowchart"
  intents: "draw_architecture"
  intents: "draw_sequence"
  patterns: "(draw|create|make) .*(diagram|flowchart|arch|sequence)"
  patterns: "excalidraw"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "terminal"
    required: false
  }
}

provides {
  capabilities: "excalidraw_diagrams"
  output_types: ".excalidraw"
}

actions {
  id: "create_diagram"
  description: "User wants to create an Excalidraw diagram"
  trigger_phrases: "create a diagram"
  trigger_phrases: "draw a flowchart"
  trigger_phrases: "make an architecture diagram"
    rules {
      text: "Output is a .excalidraw JSON file — no rendering library needed"
      priority: CRITICAL
    }
    rules {
      text: "Never use 'label' property on shapes — use container binding (boundElements + containerId)"
      priority: CRITICAL
    }
    rules {
      text: "Always include fontFamily: 1 on text elements (Virgil hand-drawn font)"
      priority: CRITICAL
    }
    rules {
      text: "Wrap elements in the excalidraw envelope: {type:'excalidraw', version:2, source:'ion-agent', elements:[], appState:{viewBackgroundColor:'#ffffff'}}"
      priority: HIGH
    }
    rules {
      text: "Array order = z-order. Emit: background zones → shape → its bound text → its arrows → next shape"
      priority: HIGH
    }
    rules {
      text: "Minimum fontSize: 16 for body, 20 for titles, 14 for annotations. Never below 14"
      priority: HIGH
    }
    rules {
      text: "Minimum shape size: 120x60 for labeled shapes. Leave 20-30px gaps between elements"
      priority: HIGH
    }
    rules {
      text: "Use color palette consistently: Primary=#a5d8ff, Success=#b2f2bb, Warning=#ffd8a8, Processing=#d0bfff, Error=#ffc9c9, Notes=#fff3bf, Storage=#c3fae8"
      priority: NORMAL
    }
    rules {
      text: "Text contrast is critical — never light gray on white. Min text color on white: #757575"
      priority: NORMAL
    }
    rules {
      text: "No emoji in text — they don't render in Excalidraw's font"
      priority: NORMAL
    }
    data {
      key: "envelope_template"
      map_value {
        entries {
          key: "type"
          string_value: "excalidraw"
        }
        entries {
          key: "version"
          int_value: 2
        }
        entries {
          key: "source"
          string_value: "ion-agent"
        }
        entries {
          key: "appState"
          string_value: "{'viewBackgroundColor':'#ffffff'}"
        }
      }
    }
    data {
      key: "element_types"
      list_value {
        items {
          string_value: "rectangle"
        }
        items {
          string_value: "ellipse"
        }
        items {
          string_value: "diamond"
        }
        items {
          string_value: "text"
        }
        items {
          string_value: "arrow"
        }
      }
    }
    data {
      key: "color_palette"
      map_value {
        entries {
          key: "primary"
          string_value: "#a5d8ff"
        }
        entries {
          key: "success"
          string_value: "#b2f2bb"
        }
        entries {
          key: "warning"
          string_value: "#ffd8a8"
        }
        entries {
          key: "processing"
          string_value: "#d0bfff"
        }
        entries {
          key: "error"
          string_value: "#ffc9c9"
        }
        entries {
          key: "notes"
          string_value: "#fff3bf"
        }
        entries {
          key: "storage"
          string_value: "#c3fae8"
        }
      }
    }
    data {
      key: "arrow_head_types"
      list_value {
        items {
          string_value: "arrow"
        }
        items {
          string_value: "bar"
        }
        items {
          string_value: "dot"
        }
        items {
          string_value: "triangle"
        }
      }
    }
    data {
      key: "fixed_point_presets"
      map_value {
        entries {
          key: "top"
          string_value: "[0.5, 0]"
        }
        entries {
          key: "bottom"
          string_value: "[0.5, 1]"
        }
        entries {
          key: "left"
          string_value: "[0, 0.5]"
        }
        entries {
          key: "right"
          string_value: "[1, 0.5]"
        }
      }
    }
    examples {
      label: "labeled rectangle with container binding"
      language: "json"
      code: "{\"type\":\"rectangle\",\"id\":\"r1\",\"x\":100,\"y\":100,\"width\":200,\"height\":80,\n \"roundness\":{\"type\":3},\"backgroundColor\":\"#a5d8ff\",\"fillStyle\":\"solid\",\n \"boundElements\":[{\"id\":\"t_r1\",\"type\":\"text\"}]}\n{\"type\":\"text\",\"id\":\"t_r1\",\"x\":105,\"y\":110,\"width\":190,\"height\":25,\n \"text\":\"Hello\",\"fontSize\":20,\"fontFamily\":1,\"strokeColor\":\"#1e1e1e\",\n \"textAlign\":\"center\",\"verticalAlign\":\"middle\",\n \"containerId\":\"r1\",\"originalText\":\"Hello\",\"autoResize\":true}"
    }
    examples {
      label: "arrow connecting two shapes"
      language: "json"
      code: "{\"type\":\"arrow\",\"id\":\"a1\",\"x\":300,\"y\":150,\"width\":150,\"height\":0,\n \"points\":[[0,0],[150,0]],\"endArrowhead\":\"arrow\",\n \"startBinding\":{\"elementId\":\"r1\",\"fixedPoint\":[1,0.5]},\n \"endBinding\":{\"elementId\":\"r2\",\"fixedPoint\":[0,0.5]}}"
    }
}
actions {
  id: "upload_diagram"
  description: "User wants to upload an excalidraw file for a shareable link"
  trigger_phrases: "upload diagram"
  trigger_phrases: "share diagram"
  trigger_phrases: "shareable link"
    rules {
      text: "Run scripts/upload.py via terminal. Requires cryptography pip package"
      priority: HIGH
    }
    data {
      key: "upload_command"
      string_value: "python skills/diagramming/excalidraw/scripts/upload.py <file.excalidraw>"
    }
}

guardrails {
  text: "Never use 'label' property on shapes — always use container binding pattern"
  scope: ALWAYS
}

guardrails {
  text: "Save as .excalidraw file via write_file"
  scope: ALWAYS
}

meta {
  name: "architecture-diagram"
  version: "1.0.0"
  summary: "Dark-themed SVG architecture/cloud/infra diagrams as self-contained HTML"
  author: "Cocoon AI, Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "architecture diagram"
  keywords: "cloud diagram"
  keywords: "infrastructure diagram"
  keywords: "SVG diagram"
  keywords: "system diagram"
  intents: "create_architecture_diagram"
  intents: "draw_infrastructure"
  intents: "visualize_architecture"
  patterns: "(create|draw|make|build) .*(architecture|infra|system) .*(diagram|chart|visual)"
  patterns: "(cloud|microservice|database) .*(diagram|topology|map)"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
}

provides {
  capabilities: "architecture_diagrams"
  capabilities: "svg_generation"
  capabilities: "dark_themed_diagrams"
  output_types: ".html"
}

actions {
  id: "create_diagram"
  description: "Generate a dark-themed architecture diagram as self-contained HTML with inline SVG"
  trigger_phrases: "create architecture diagram"
  trigger_phrases: "draw system diagram"
  trigger_phrases: "build infrastructure diagram"
    rules {
      text: "Single self-contained .html file — no external dependencies except Google Fonts"
      priority: CRITICAL
    }
    rules {
      text: "No JavaScript — use pure CSS for animations (pulsing dots)"
      priority: CRITICAL
    }
    rules {
      text: "Font: JetBrains Mono from Google Fonts. Sizes: 12px names, 9px sublabels, 8px annotations"
      priority: HIGH
    }
    rules {
      text: "Background: Slate-950 (#020617) with 40px grid pattern"
      priority: HIGH
    }
    rules {
      text: "Components are rounded rectangles (rx=6) with 1.5px strokes — use double-rect masking for transparency"
      priority: HIGH
    }
    rules {
      text: "Draw arrows early in SVG (after grid) so they render behind component boxes"
      priority: HIGH
    }
    rules {
      text: "Color palette: Frontend=#22d3ee, Backend=#34d399, Database=#a78bfa, AWS=#fbbf24, Security=#fb7185, MessageBus=#fb923c, External=#94a3b8"
      priority: NORMAL
    }
    rules {
      text: "Legend must be placed outside all boundary boxes — at least 20px below lowest boundary"
      priority: NORMAL
    }
    data {
      key: "color_palette"
      map_value {
        entries {
          key: "frontend"
          string_value: "#22d3ee"
        }
        entries {
          key: "backend"
          string_value: "#34d399"
        }
        entries {
          key: "database"
          string_value: "#a78bfa"
        }
        entries {
          key: "aws_cloud"
          string_value: "#fbbf24"
        }
        entries {
          key: "security"
          string_value: "#fb7185"
        }
        entries {
          key: "message_bus"
          string_value: "#fb923c"
        }
        entries {
          key: "external"
          string_value: "#94a3b8"
        }
      }
    }
    data {
      key: "background_color"
      string_value: "#020617"
    }
    data {
      key: "grid_size"
      int_value: 40
    }
    data {
      key: "document_structure"
      list_value {
        items {
          string_value: "Header: title with pulsing dot + subtitle"
        }
        items {
          string_value: "Main SVG: diagram in rounded border card"
        }
        items {
          string_value: "Summary Cards: 3-card grid below diagram"
        }
        items {
          string_value: "Footer: minimal metadata"
        }
      }
    }
    examples {
      label: "info card pattern"
      language: "html"
      code: "<div class=\"card\">\n  <div class=\"card-header\">\n    <div class=\"card-dot cyan\"></div>\n    <h3>Title</h3>\n  </div>\n  <ul><li>• Item one</li><li>• Item two</li></ul>\n</div>"
    }
}

guardrails {
  text: "Output is a single .html file — no external rendering libraries needed"
  scope: ALWAYS
}

guardrails {
  text: "Must render correctly in any modern browser offline"
  scope: ALWAYS
}

related {
  name: "concept-diagrams"
}

related {
  name: "excalidraw"
}

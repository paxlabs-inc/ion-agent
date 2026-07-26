meta {
  name: "pretext"
  version: "1.0.0"
  summary: "Creative browser demos with @chenglou/pretext — DOM-free text layout for ASCII art, kinetic typography, text-as-geometry"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "pretext"
  keywords: "text layout"
  keywords: "ascii art"
  keywords: "kinetic typography"
  keywords: "text-as-geometry"
  keywords: "text reflow"
  keywords: "chenglou"
  intents: "create_pretext_demo"
  intents: "text_reflow"
  intents: "ascii_typography"
  intents: "kinetic_type_demo"
  patterns: "pretext"
  patterns: "(text|ascii) .*(reflow|flow around|obstacle)"
  patterns: "kinetic typography"
  patterns: "text-as-(geometry|game)"
  patterns: "(shrink-wrap|multiline) .*(text|ui)"
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
  capabilities: "text_reflow_demos"
  capabilities: "ascii_typography"
  capabilities: "kinetic_type"
  capabilities: "text_geometry_games"
  output_types: ".html"
}

actions {
  id: "build_pretext_demo"
  description: "Build a creative demo using @chenglou/pretext library"
  trigger_phrases: "build pretext demo"
  trigger_phrases: "create text reflow"
  trigger_phrases: "pretext creative demo"
    rules {
      text: "Single self-contained HTML file. Import from esm.sh with pinned version: @chenglou/pretext@0.0.6."
      priority: CRITICAL
    }
    rules {
      text: "Don't ship a hello world — add intentional color, motion, composition, and one extra detail."
      priority: CRITICAL
    }
    rules {
      text: "Dark backgrounds, warm cores, considered palette. Never default white canvas."
      priority: CRITICAL
    }
    rules {
      text: "Proportional fonts are the point — lean into it. Never default sans or monospace."
      priority: HIGH
    }
    rules {
      text: "Real source text, not lorem ipsum. Short manifestos, poetry, real code, found text."
      priority: HIGH
    }
    rules {
      text: "First-paint excellence — no loading states, no blank frames. Must look shippable instantly."
      priority: HIGH
    }
    rules {
      text: "Keep font string passed to prepare() exactly matching CSS font — drift causes measurement errors."
      priority: NORMAL
    }
    data {
      key: "cdn_import"
      string_value: "https://esm.sh/@chenglou/pretext@0.0.6"
    }
    data {
      key: "demo_patterns"
      map_value {
        entries {
          key: "reflow_around_obstacle"
          string_value: "layoutNextLineRange + per-row width function"
        }
        entries {
          key: "text_as_geometry_game"
          string_value: "layoutWithLines + per-line collision rects"
        }
        entries {
          key: "shatter_particles"
          string_value: "walkLineRanges → per-grapheme (x,y) → physics"
        }
        entries {
          key: "ascii_obstacle_typography"
          string_value: "layoutNextLineRange + measured per-row obstacle spans"
        }
        entries {
          key: "editorial_multicolumn"
          string_value: "layoutNextLineRange per column + shared cursor"
        }
        entries {
          key: "kinetic_type"
          string_value: "layoutWithLines + per-line transform over time"
        }
        entries {
          key: "multiline_shrinkwrap"
          string_value: "measureLineStats"
        }
      }
    }
    examples {
      label: "basic import and measure"
      language: "javascript"
      code: "import { prepare, layout, prepareWithSegments, layoutWithLines, layoutNextLineRange, materializeLineRange } from \"https://esm.sh/@chenglou/pretext@0.0.6\";\n// Use-case 1: measure height\nconst prepared = prepare(text, \"16px Inter\");\nconst { height, lineCount } = layout(prepared, 320, 20);\n// Use-case 2: measure and render yourself\nconst prepared2 = prepareWithSegments(text, \"16px Inter\");\nconst { lines } = layoutWithLines(prepared2, 320, 26);\nfor (let i = 0; i < lines.length; i++) { ctx.fillText(lines[i].text, 0, i * 26); }"
    }
}
actions {
  id: "variable_width_reflow"
  description: "Flow text around a moving shape with variable width per line"
  trigger_phrases: "text around shape"
  trigger_phrases: "reflow around obstacle"
  trigger_phrases: "variable width text"
    rules {
      text: "Most important pattern: use layoutNextLineRange with a widthAtY(y) function for variable-width corridors."
      priority: CRITICAL
    }
    rules {
      text: "If corridor too narrow for a line, skip the row (y += lineHeight; continue) not passing tiny maxWidth."
      priority: HIGH
    }
    rules {
      text: "prepare() is expensive — call once per text+font pair. Only layout* is cheap enough for per-frame."
      priority: HIGH
    }
    rules {
      text: "Canvas ctx.font setting is slow — set once per frame if font doesn't vary."
      priority: NORMAL
    }
    examples {
      label: "variable width reflow loop"
      language: "javascript"
      code: "let cursor = { segmentIndex: 0, graphemeIndex: 0 };\nlet y = 0;\nwhile (true) {\n  const lineWidth = widthAtY(y);\n  const range = layoutNextLineRange(prepared, cursor, lineWidth);\n  if (!range) break;\n  const line = materializeLineRange(prepared, range);\n  ctx.fillText(line.text, leftEdgeAtY(y), y);\n  cursor = range.end;\n  y += lineHeight;\n}"
    }
}

guardrails {
  text: "Use esm.sh for import — unpkg will 404 or serve raw TS"
  scope: ALWAYS
}

guardrails {
  text: "Never re-prepare inside animation loop — only layout* is cheap"
  scope: ALWAYS
}

guardrails {
  text: "Use Intl.Segmenter for grapheme splits — .split('') breaks emoji/CJK"
  scope: ALWAYS
}

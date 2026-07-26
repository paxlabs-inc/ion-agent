meta {
  name: "sketch"
  version: "1.0.0"
  summary: "Throwaway HTML mockups — 2-3 design variants to compare visual directions"
  author: "Ion Agent (adapted from gsd-build/get-shit-done)"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "sketch"
  keywords: "mockup"
  keywords: "design variant"
  keywords: "prototype"
  keywords: "wireframe"
  keywords: "UI exploration"
  keywords: "design comparison"
  intents: "create_mockups"
  intents: "compare_designs"
  intents: "explore_ui_directions"
  patterns: "(sketch|mockup|prototype) .*(screen|ui|page|layout)"
  patterns: "(show|compare) .*(variants|designs|takes|directions)"
  patterns: "(2|3|two|three) .*(variants|versions|takes)"
  patterns: "what .*(could look|might look) like"
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
  capabilities: "design_mockups"
  capabilities: "variant_comparison"
  capabilities: "throwaway_prototypes"
  output_types: ".html"
}

actions {
  id: "intake_design"
  description: "Gather feel, references, and core action before generating variants"
  trigger_phrases: "design intake"
  trigger_phrases: "gather requirements"
  trigger_phrases: "what should this feel like"
    rules {
      text: "Gather three things one question at a time: 1) Feel (adjectives, vibe), 2) References (apps/sites that capture it), 3) Core action (single most important user action)."
      priority: HIGH
    }
    rules {
      text: "Reflect each answer briefly before the next question. Skip if user already gave all three."
      priority: HIGH
    }
    rules {
      text: "'calm, editorial, like Linear' tells you more than 'minimal'."
      priority: NORMAL
    }
}
actions {
  id: "generate_variants"
  description: "Build 2-3 HTML variants with different design stances"
  trigger_phrases: "create variants"
  trigger_phrases: "build mockups"
  trigger_phrases: "show me options"
  trigger_phrases: "design variants"
    rules {
      text: "Produce 2-3 variants, never 1, rarely 4+. Each is a complete standalone HTML file."
      priority: CRITICAL
    }
    rules {
      text: "Each variant must take a DIFFERENT design stance, not different pixel values. Pick one axis and pull apart."
      priority: CRITICAL
    }
    rules {
      text: "Good variant axes: density (compact/airy), emphasis (content/action/first), aesthetic (editorial/utilitarian/playful), layout (single-column/sidebar/split)."
      priority: HIGH
    }
    rules {
      text: "Single self-contained HTML: inline <style>, system fonts or one Google Font, realistic fake content, interactive (clickable, hovers, one state transition)."
      priority: HIGH
    }
    rules {
      text: "Verify visually with browser_vision — don't just write HTML and hope."
      priority: HIGH
    }
    rules {
      text: "Variant naming: describe stance not number. 001-calm-editorial, 002-utilitarian-dense, 003-playful-split."
      priority: NORMAL
    }
    data {
      key: "variant_axes"
      list_value {
        items {
          string_value: "density: compact / airy / ultra-dense"
        }
        items {
          string_value: "emphasis: content-first / action-first / tool-first"
        }
        items {
          string_value: "aesthetic: editorial / utilitarian / playful"
        }
        items {
          string_value: "layout: single-column / sidebar / split-pane"
        }
        items {
          string_value: "grounding: card-based / bare-content / document-style"
        }
      }
    }
    examples {
      label: "default CSS reset for fast starts"
      language: "html"
      code: "<style>\n  * { box-sizing: border-box; margin: 0; padding: 0; }\n  body { font-family: -apple-system, BlinkMacSystemFont, \"Segoe UI\", Roboto, \"Helvetica Neue\", Arial, sans-serif;\n         -webkit-font-smoothing: antialiased; color: #1a1a1a; background: #fafafa; line-height: 1.5; }\n</style>"
    }
}
actions {
  id: "head_to_head_comparison"
  description: "Present variants as opinionated comparison table"
  trigger_phrases: "compare variants"
  trigger_phrases: "which is best"
  trigger_phrases: "head to head"
    rules {
      text: "Present comparison table with dimensions: density, primary action visibility, scan-ability, feel."
      priority: HIGH
    }
    rules {
      text: "Opinionate — don't just list. State which is strongest and why."
      priority: HIGH
    }
    rules {
      text: "Let user pick winner, combine two into hybrid, or ask for another round."
      priority: NORMAL
    }
}
actions {
  id: "variant_readme"
  description: "Write README for each variant documenting design stance"
  trigger_phrases: "variant readme"
  trigger_phrases: "document the variant"
    rules {
      text: "Each README answers: design stance (one sentence), key choices (layout/typography/color/interaction), trade-offs, best for."
      priority: NORMAL
    }
    data {
      key: "readme_sections"
      list_value {
        items {
          string_value: "design_stance"
        }
        items {
          string_value: "key_choices"
        }
        items {
          string_value: "trade_offs"
        }
        items {
          string_value: "best_for"
        }
      }
    }
}

guardrails {
  text: "Variants must differ in design stance, not just accent color — user must be able to distinguish them"
  scope: ALWAYS
}

guardrails {
  text: "Open HTML in browser and verify with browser_vision before showing to user"
  scope: ALWAYS
}

guardrails {
  text: "Keep variants disposable — promote to real code only if user wants to keep one"
  scope: ALWAYS
}

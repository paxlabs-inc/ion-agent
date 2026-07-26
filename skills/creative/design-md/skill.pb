meta {
  name: "design-md"
  version: "1.0.0"
  summary: "Author, validate, lint, diff, and export Google's DESIGN.md token spec files"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "DESIGN.md"
  keywords: "design tokens"
  keywords: "design system spec"
  keywords: "WCAG contrast"
  keywords: "Tailwind export"
  keywords: "DTCG"
  intents: "author_design_md"
  intents: "lint_design_md"
  intents: "export_design_tokens"
  intents: "validate_contrast"
  patterns: "(create|author|write) .*(DESIGN.md|design tokens|design system spec)"
  patterns: "(lint|validate|diff|export) .*(DESIGN.md|design tokens)"
  patterns: "(WCAG|contrast) .*(check|validate|accessibility)"
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
  binaries: "npx"
}

provides {
  capabilities: "design_token_authoring"
  capabilities: "design_md_linting"
  capabilities: "wcag_validation"
  capabilities: "token_export"
  output_types: ".md"
  output_types: ".json"
}

actions {
  id: "author_design_md"
  description: "Create a new DESIGN.md file with YAML front matter and markdown body"
  trigger_phrases: "create DESIGN.md"
  trigger_phrases: "author design tokens"
  trigger_phrases: "write design spec"
    rules {
      text: "YAML front matter has machine-readable tokens (normative values); markdown body has human-readable rationale"
      priority: CRITICAL
    }
    rules {
      text: "Always include name: and colors:; other sections optional but encouraged"
      priority: HIGH
    }
    rules {
      text: "Use token references ({colors.primary}) in components section — single-source palette"
      priority: HIGH
    }
    rules {
      text: "Canonical section order: Overview, Colors, Typography, Layout, Elevation & Depth, Shapes, Components, Do's and Don'ts"
      priority: HIGH
    }
    rules {
      text: "Hex colors must be quoted strings in YAML — # starts comments otherwise"
      priority: NORMAL
    }
    data {
      key: "canonical_sections"
      list_value {
        items {
          string_value: "Overview"
        }
        items {
          string_value: "Colors"
        }
        items {
          string_value: "Typography"
        }
        items {
          string_value: "Layout"
        }
        items {
          string_value: "Elevation & Depth"
        }
        items {
          string_value: "Shapes"
        }
        items {
          string_value: "Components"
        }
        items {
          string_value: "Do's and Don'ts"
        }
      }
    }
    data {
      key: "token_types"
      map_value {
        entries {
          key: "color"
          string_value: "# + hex (sRGB)"
        }
        entries {
          key: "dimension"
          string_value: "number + unit (px, em, rem)"
        }
        entries {
          key: "reference"
          string_value: "{path.to.token}"
        }
        entries {
          key: "typography"
          string_value: "object with fontFamily, fontSize, fontWeight, lineHeight, letterSpacing"
        }
      }
    }
    data {
      key: "component_properties"
      list_value {
        items {
          string_value: "backgroundColor"
        }
        items {
          string_value: "textColor"
        }
        items {
          string_value: "typography"
        }
        items {
          string_value: "rounded"
        }
        items {
          string_value: "padding"
        }
        items {
          string_value: "size"
        }
        items {
          string_value: "height"
        }
        items {
          string_value: "width"
        }
      }
    }
}
actions {
  id: "lint_export"
  description: "Lint, diff, and export DESIGN.md files using the CLI"
  trigger_phrases: "lint DESIGN.md"
  trigger_phrases: "export tokens"
  trigger_phrases: "diff design"
    rules {
      text: "CLI: npx @google/design.md — lint, diff, export commands"
      priority: HIGH
    }
    rules {
      text: "Lint catches: broken references, duplicate sections, invalid types, WCAG contrast violations"
      priority: HIGH
    }
    rules {
      text: "Export formats: --format tailwind (tailwind.theme.json), --format dtcg (tokens.json)"
      priority: NORMAL
    }
    rules {
      text: "All commands accept - for stdin; lint returns exit 1 on errors"
      priority: NORMAL
    }
    data {
      key: "cli_commands"
      list_value {
        items {
          string_value: "npx -y @google/design.md lint DESIGN.md"
        }
        items {
          string_value: "npx -y @google/design.md diff DESIGN.md DESIGN-v2.md"
        }
        items {
          string_value: "npx -y @google/design.md export --format tailwind DESIGN.md"
        }
        items {
          string_value: "npx -y @google/design.md export --format dtcg DESIGN.md"
        }
      }
    }
    data {
      key: "lint_rules"
      list_value {
        items {
          string_value: "broken-ref (error) — non-existent token reference"
        }
        items {
          string_value: "duplicate-section (error) — same heading twice"
        }
        items {
          string_value: "invalid-color/dimension/typography (error)"
        }
        items {
          string_value: "wcag-contrast (warning) — component textColor vs backgroundColor"
        }
        items {
          string_value: "unknown-component-property (warning)"
        }
      }
    }
}

guardrails {
  text: "Don't nest component variants — use sibling keys (button-primary-hover, not button-primary.hover)"
  scope: ALWAYS
}

guardrails {
  text: "Section order is enforced — reorder prose to match canonical list before saving"
  scope: ALWAYS
}

guardrails {
  text: "Token references resolve by dotted path — {colors.primary} works, {primary} does not"
  scope: ALWAYS
}

related {
  name: "popular-web-designs"
}

related {
  name: "claude-design"
}

related {
  name: "excalidraw"
}

related {
  name: "architecture-diagram"
}

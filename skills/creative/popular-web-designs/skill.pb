meta {
  name: "popular-web-designs"
  version: "1.0.0"
  summary: "54 production-ready design systems (Stripe, Linear, Vercel) as HTML/CSS templates"
  author: "Ion Agent + Teknium"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "design system"
  keywords: "web design"
  keywords: "landing page"
  keywords: "dashboard design"
  keywords: "stripe style"
  keywords: "linear style"
  keywords: "vercel style"
  keywords: "UI template"
  intents: "style_like_brand"
  intents: "create_styled_ui"
  intents: "match_design_system"
  patterns: "(build|create|make) .*(page|site|ui) .*(looks like|styled like|like) .*(stripe|linear|vercel|apple|figma)"
  patterns: "(stripe|linear|vercel|apple|figma|notion) .*(style|design|aesthetic)"
  patterns: "web design"
  patterns: "landing page"
  patterns: "dashboard design"
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
  capabilities: "design_system_templates"
  capabilities: "brand_styled_html"
  capabilities: "font_substitution"
  output_types: ".html"
  output_types: ".css"
}

actions {
  id: "select_design"
  description: "Choose a design system from the 54-template catalog"
  trigger_phrases: "pick a design"
  trigger_phrases: "choose a template"
  trigger_phrases: "which design system"
    rules {
      text: "Load template: skill_view(name='popular-web-designs', file_path='templates/<site>.md'). Each template has color palette, typography, components, spacing, shadows."
      priority: CRITICAL
    }
    rules {
      text: "Match design to content: dev tools → Linear/Vercel/Supabase, docs → Mintlify/Notion, marketing → Stripe/Apple, dark UIs → Cursor/ElevenLabs."
      priority: HIGH
    }
    rules {
      text: "Pair with claude-design for design process and taste, this skill supplies visual vocabulary."
      priority: HIGH
    }
    rules {
      text: "Pair with generative-widgets skill to serve via cloudflared tunnel."
      priority: NORMAL
    }
    data {
      key: "catalog_categories"
      list_value {
        items {
          string_value: "AI & Machine Learning (12 templates)"
        }
        items {
          string_value: "Developer Tools & Platforms (14 templates)"
        }
        items {
          string_value: "Infrastructure & Cloud (6 templates)"
        }
        items {
          string_value: "Design & Productivity (10 templates)"
        }
        items {
          string_value: "Fintech & Crypto (4 templates)"
        }
        items {
          string_value: "Enterprise & Consumer (8 templates)"
        }
      }
    }
    data {
      key: "style_categories"
      map_value {
        entries {
          key: "dev_dashboards"
          string_value: "Linear, Vercel, Supabase, Raycast, Sentry"
        }
        entries {
          key: "docs_content"
          string_value: "Mintlify, Notion, Sanity, MongoDB"
        }
        entries {
          key: "marketing_landing"
          string_value: "Stripe, Framer, Apple, SpaceX"
        }
        entries {
          key: "dark_mode"
          string_value: "Linear, Cursor, ElevenLabs, Warp, Superhuman"
        }
        entries {
          key: "playful"
          string_value: "PostHog, Figma, Lovable, Zapier, Miro"
        }
        entries {
          key: "premium_luxury"
          string_value: "Apple, BMW, Stripe, Superhuman, Revolut"
        }
        entries {
          key: "monospace_terminal"
          string_value: "Ollama, OpenCode, x.ai, VoltAgent"
        }
      }
    }
}
actions {
  id: "apply_font_substitution"
  description: "Map proprietary fonts to Google Fonts CDN substitutes"
  trigger_phrases: "font substitution"
  trigger_phrases: "cdn font"
  trigger_phrases: "google fonts alternative"
    rules {
      text: "When CDN font matches original (Inter, IBM Plex, Rubik, Geist), no substitution loss."
      priority: HIGH
    }
    rules {
      text: "When using a substitute, follow template weight/size/letter-spacing values closely — those carry more visual identity than the font face."
      priority: HIGH
    }
    rules {
      text: "Common mappings: Geist→Geist (Google), sohne-var→Source Sans 3, Circular→DM Sans, figmaSans→Inter."
      priority: NORMAL
    }
    data {
      key: "font_mappings"
      map_value {
        entries {
          key: "geist"
          string_value: "Geist (Google Fonts)"
        }
        entries {
          key: "sohne_var"
          string_value: "Source Sans 3"
        }
        entries {
          key: "circular"
          string_value: "DM Sans"
        }
        entries {
          key: "figmaSans"
          string_value: "Inter"
        }
        entries {
          key: "berkeley_mono"
          string_value: "JetBrains Mono"
        }
        entries {
          key: "airbnb_cereal"
          string_value: "DM Sans"
        }
      }
    }
}
actions {
  id: "generate_styled_html"
  description: "Generate HTML/CSS using design system tokens from a template"
  trigger_phrases: "generate styled page"
  trigger_phrases: "build with design tokens"
  trigger_phrases: "create styled html"
    rules {
      text: "Apply template color palette as CSS custom properties in :root. Apply typography from template Section 3."
      priority: CRITICAL
    }
    rules {
      text: "Write with write_file, serve with generative-widgets (cloudflared tunnel), verify with browser_vision."
      priority: HIGH
    }
    rules {
      text: "Each template includes Ion Implementation Notes with CDN font substitute and Google Fonts link tag."
      priority: NORMAL
    }
    examples {
      label: "html generation pattern"
      language: "html"
      code: "<!DOCTYPE html><html><head>\n<meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1.0\">\n<title>Page Title</title>\n<link href=\"https://fonts.googleapis.com/css2?family=...\" rel=\"stylesheet\">\n<style>\n  :root { --color-bg: #ffffff; --color-text: #171717; --color-accent: #533afd; }\n  body { font-family: 'Inter', system-ui, sans-serif; color: var(--color-text); background: var(--color-bg); }\n</style>\n</head><body><!-- Build using component specs from template --></body></html>"
    }
}

guardrails {
  text: "Load template before generating — never guess design tokens"
  scope: ALWAYS
}

guardrails {
  text: "Verify rendered output with browser_vision — don't just write HTML and hope"
  scope: ALWAYS
}

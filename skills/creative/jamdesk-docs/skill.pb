meta {
  name: "jamdesk-docs"
  version: "1.0.0"
  summary: "Jamdesk documentation sites — CSS variables, navigation, bulk docs rewrite, brand tokens, llms.txt"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "jamdesk"
  keywords: "docs site"
  keywords: "documentation website"
  keywords: "docs.json"
  keywords: "developer documentation"
  keywords: "API docs"
  keywords: "docs rewrite"
  intents: "create_docs_site"
  intents: "rewrite_docs"
  intents: "configure_jamdesk"
  intents: "bulk_docs_rewrite"
  patterns: "(create|build|configure) .*(docs|documentation) .*(site|website)"
  patterns: "jamdesk"
  patterns: "docs.json"
  patterns: "(bulk|mass) .*(rewrite|update) .*(docs|documentation)"
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
  capabilities: "jamdesk_docs_sites"
  capabilities: "bulk_docs_rewrite"
  capabilities: "brand_token_integration"
  output_types: ".mdx"
  output_types: ".json"
  output_types: ".css"
}

actions {
  id: "configure_docs_json"
  description: "Create or modify a Jamdesk docs.json configuration file"
  trigger_phrases: "configure docs.json"
  trigger_phrases: "set up jamdesk site"
  trigger_phrases: "create docs.json"
    rules {
      text: "docs.json goes at the project root. Required fields: name, theme, colors, navigation."
      priority: CRITICAL
    }
    rules {
      text: "Navigation hierarchy: Tabs > Groups > Pages. Each page references an .mdx file path (without extension)."
      priority: HIGH
    }
    rules {
      text: "Standard tab split: Documentation (general users), Developer (devs/ops), Reference (per-module deep docs), For Agents (llms.txt, sitemap)."
      priority: HIGH
    }
    rules {
      text: "appearance.default controls light/dark mode. appearance.strict locks to one mode."
      priority: NORMAL
    }
    data {
      key: "docs_json_schema"
      string_value: "https://jamdesk.com/docs.json"
    }
    data {
      key: "required_fields"
      list_value {
        items {
          string_value: "name"
        }
        items {
          string_value: "theme"
        }
        items {
          string_value: "colors"
        }
        items {
          string_value: "navigation"
        }
      }
    }
    data {
      key: "navigation_example"
      map_value {
        entries {
          key: "tabs"
          string_value: "[{tab:\"Documentation\",groups:[{group:\"Get Started\",pages:[\"introduction\",\"quickstart\"]}]},{tab:\"Developer\",groups:[...]}]"
        }
      }
    }
}
actions {
  id: "configure_css_variables"
  description: "Set up Jamdesk CSS variable schema via style.css"
  trigger_phrases: "jamdesk css"
  trigger_phrases: "style.css jamdesk"
  trigger_phrases: "configure jamdesk theme"
  trigger_phrases: "brand tokens"
    rules {
      text: "Place style.css in project root (same dir as docs.json). No config entry needed."
      priority: CRITICAL
    }
    rules {
      text: "Dark-only sites: define values in :root AND duplicate in [data-theme='dark'] as safety."
      priority: CRITICAL
    }
    rules {
      text: "Required variables: --font-family-sans, --font-family-mono, --content-max-width, --sidebar-width, --radius-sm/md/lg, --color-background, --color-text, --color-text-muted, --color-border, --color-code-bg, --color-primary, --color-primary-subtle."
      priority: HIGH
    }
    rules {
      text: "Brand token mapping: --color-background maps to surface base, --color-text to foreground, --color-primary to single accent."
      priority: HIGH
    }
    rules {
      text: "Component targeting via data-component and data-callout attributes."
      priority: NORMAL
    }
    data {
      key: "required_variables"
      list_value {
        items {
          string_value: "--font-family-sans"
        }
        items {
          string_value: "--font-family-mono"
        }
        items {
          string_value: "--content-max-width"
        }
        items {
          string_value: "--sidebar-width"
        }
        items {
          string_value: "--color-background"
        }
        items {
          string_value: "--color-text"
        }
        items {
          string_value: "--color-text-muted"
        }
        items {
          string_value: "--color-border"
        }
        items {
          string_value: "--color-primary"
        }
        items {
          string_value: "--color-primary-subtle"
        }
      }
    }
    data {
      key: "component_selectors"
      list_value {
        items {
          string_value: "[data-component=\"card\"]"
        }
        items {
          string_value: "[data-component=\"code-block\"]"
        }
        items {
          string_value: "[data-callout=\"note\"]"
        }
        items {
          string_value: "[data-callout=\"warning\"]"
        }
      }
    }
    examples {
      label: "minimal style.css for dark-only site"
      language: "css"
      code: ":root {\n  --font-family-sans: 'Inter', system-ui, sans-serif;\n  --font-family-mono: 'JetBrains Mono', monospace;\n  --content-max-width: 900px;\n  --sidebar-width: 280px;\n  --radius-sm: 4px; --radius-md: 8px; --radius-lg: 12px;\n  --color-background: #0a0a0a; --color-text: #fafafa;\n  --color-text-muted: #a3a3a3; --color-border: #262626;\n  --color-code-bg: #171717; --color-primary: #hex; --color-primary-subtle: #hex24;\n}"
    }
}
actions {
  id: "bulk_docs_rewrite"
  description: "Perform large-scale rewrites across 50+ .mdx files using parallel subagents"
  trigger_phrases: "rewrite docs"
  trigger_phrases: "bulk docs update"
  trigger_phrases: "refresh documentation"
  trigger_phrases: "docs rewrite workflow"
    rules {
      text: "Workflow: 1) Clone source repo, 2) Inspect architecture, 3) Bulk mechanical fixes via execute_code, 4) Dispatch subagents in batches of 3, 5) Update docs.json nav, 6) Apply style.css, 7) Verify."
      priority: CRITICAL
    }
    rules {
      text: "Organize subagent work by logical clusters (consumer, developer, API reference) not alphabetically."
      priority: HIGH
    }
    rules {
      text: "Each subagent needs: source code file paths, current doc file paths, explicit rules (no em dashes, maintain mdx format)."
      priority: HIGH
    }
    rules {
      text: "Consumer pages: intro, quickstart, user-guide, FAQ. Developer pages: architecture, module deep-dives."
      priority: NORMAL
    }
    data {
      key: "batch_strategy"
      map_value {
        entries {
          key: "batch_1"
          string_value: "Consumer pages (intro, quickstart, user-guide, FAQ)"
        }
        entries {
          key: "batch_2"
          string_value: "Developer pages (architecture, module deep-dives)"
        }
        entries {
          key: "batch_3"
          string_value: "API reference pages"
        }
        entries {
          key: "batch_4_plus"
          string_value: "Reference docs (split by module, 3 subagents per batch)"
        }
      }
    }
}
actions {
  id: "ai_discoverability"
  description: "Add llms.txt, MCP server, and AI chat to docs site"
  trigger_phrases: "llms.txt"
  trigger_phrases: "ai discoverability"
  trigger_phrases: "agent-friendly docs"
    rules {
      text: "llms.txt is a machine-readable doc index allowing AI agents to discover pages."
      priority: HIGH
    }
    rules {
      text: "Support .md suffix on any URL returning raw markdown version."
      priority: HIGH
    }
    rules {
      text: "Consider MCP server for docs enabling AI tools to query programmatically."
      priority: NORMAL
    }
}
actions {
  id: "verify_docs"
  description: "Verify documentation site integrity after build or rewrite"
  trigger_phrases: "verify docs"
  trigger_phrases: "check docs integrity"
  trigger_phrases: "docs validation"
    rules {
      text: "Check: 1) docs.json valid JSON, 2) All nav pages exist as .mdx, 3) No orphan .mdx files, 4) Zero em dashes, 5) style.css tokens present."
      priority: CRITICAL
    }
    rules {
      text: "Pages referenced in docs.json navigation MUST have corresponding .mdx files or build fails silently."
      priority: HIGH
    }
    rules {
      text: "Multiple .css files in project root are merged alphabetically by filename."
      priority: NORMAL
    }
    examples {
      label: "verification script"
      language: "python"
      code: "import json, os\ndocs_dir = '/path/to/docs'\nwith open(os.path.join(docs_dir, 'docs.json')) as f:\n    config = json.load(f)\nnav = config['navigation']['tabs']\npage_refs = set()\nfor tab in nav:\n    for group in tab.get('groups', []):\n        for page in group.get('pages', []):\n            page_refs.add(page + '.mdx')\n            assert os.path.exists(os.path.join(docs_dir, page + '.mdx')), f\"MISSING: {page}\""
    }
}

guardrails {
  text: "Jamdesk applies custom CSS globally — use specific selectors to prevent conflicts"
  scope: ALWAYS
}

guardrails {
  text: "Pages in docs.json navigation must have matching .mdx files — build fails silently otherwise"
  scope: ALWAYS
}

guardrails {
  text: "jamdesk dev applies CSS on browser refresh; published site applies on next build"
  scope: ALWAYS
}

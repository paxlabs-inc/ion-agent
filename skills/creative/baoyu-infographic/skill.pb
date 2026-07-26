meta {
  name: "baoyu-infographic"
  version: "1.56.1"
  summary: "Generate infographics with 21 layouts x 21 styles — visual summaries and information graphics"
  author: "宝玉 (JimLiu)"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "infographic"
  keywords: "visual summary"
  keywords: "information graphic"
  keywords: "信息图"
  keywords: "可视化"
  keywords: "高密度信息大图"
  intents: "create_infographic"
  intents: "visual_summary"
  intents: "information_graphic"
  patterns: "(create|make|generate) .*(infographic|visual summary|information graphic)"
  patterns: "信息图|可视化|高密度信息大图"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "image_generate"
    required: true
  }
}

provides {
  capabilities: "infographic_generation"
  capabilities: "layout_style_combinations"
  capabilities: "visual_summaries"
  output_types: ".png"
  output_types: ".md"
}

actions {
  id: "analyze_content"
  description: "Analyze source content for topic, data type, complexity, tone, audience"
  trigger_phrases: "analyze content"
  trigger_phrases: "analyze source"
  trigger_phrases: "review content"
    rules {
      text: "Preserve source data faithfully — no summarization or rephrasing"
      priority: CRITICAL
    }
    rules {
      text: "Strip any credentials, API keys, tokens, or secrets before including in outputs"
      priority: CRITICAL
    }
    rules {
      text: "Save source content to source.md; if exists, rename to backup with timestamp"
      priority: HIGH
    }
    rules {
      text: "Detect source language and user language"
      priority: NORMAL
    }
    data {
      key: "output_structure"
      list_value {
        items {
          string_value: "source-{slug}.{ext}"
        }
        items {
          string_value: "analysis.md"
        }
        items {
          string_value: "structured-content.md"
        }
        items {
          string_value: "prompts/infographic.md"
        }
        items {
          string_value: "infographic.png"
        }
      }
    }
}
actions {
  id: "recommend_combinations"
  description: "Recommend layout x style combinations based on content analysis"
  trigger_phrases: "recommend layout"
  trigger_phrases: "suggest style"
  trigger_phrases: "pick combination"
    rules {
      text: "Check keyword shortcuts first: 高密度信息大图 → dense-modules; 信息图 → bento-grid"
      priority: HIGH
    }
    rules {
      text: "Default: bento-grid + craft-handmade"
      priority: HIGH
    }
    rules {
      text: "Recommend 3-5 combinations based on data structure, content tone, audience expectations"
      priority: NORMAL
    }
    data {
      key: "default_layout"
      string_value: "bento-grid"
    }
    data {
      key: "default_style"
      string_value: "craft-handmade"
    }
    data {
      key: "layouts"
      list_value {
        items {
          string_value: "linear-progression"
        }
        items {
          string_value: "binary-comparison"
        }
        items {
          string_value: "comparison-matrix"
        }
        items {
          string_value: "hierarchical-layers"
        }
        items {
          string_value: "tree-branching"
        }
        items {
          string_value: "hub-spoke"
        }
        items {
          string_value: "structural-breakdown"
        }
        items {
          string_value: "bento-grid"
        }
        items {
          string_value: "iceberg"
        }
        items {
          string_value: "bridge"
        }
        items {
          string_value: "funnel"
        }
        items {
          string_value: "isometric-map"
        }
        items {
          string_value: "dashboard"
        }
        items {
          string_value: "periodic-table"
        }
        items {
          string_value: "comic-strip"
        }
        items {
          string_value: "story-mountain"
        }
        items {
          string_value: "jigsaw"
        }
        items {
          string_value: "venn-diagram"
        }
        items {
          string_value: "winding-roadmap"
        }
        items {
          string_value: "circular-flow"
        }
        items {
          string_value: "dense-modules"
        }
      }
    }
    data {
      key: "styles"
      list_value {
        items {
          string_value: "craft-handmade"
        }
        items {
          string_value: "claymation"
        }
        items {
          string_value: "kawaii"
        }
        items {
          string_value: "storybook-watercolor"
        }
        items {
          string_value: "chalkboard"
        }
        items {
          string_value: "cyberpunk-neon"
        }
        items {
          string_value: "bold-graphic"
        }
        items {
          string_value: "aged-academia"
        }
        items {
          string_value: "corporate-memphis"
        }
        items {
          string_value: "technical-schematic"
        }
        items {
          string_value: "origami"
        }
        items {
          string_value: "pixel-art"
        }
        items {
          string_value: "ui-wireframe"
        }
        items {
          string_value: "subway-map"
        }
        items {
          string_value: "ikea-manual"
        }
        items {
          string_value: "knolling"
        }
        items {
          string_value: "lego-brick"
        }
        items {
          string_value: "pop-laboratory"
        }
        items {
          string_value: "morandi-journal"
        }
        items {
          string_value: "retro-pop-grid"
        }
        items {
          string_value: "hand-drawn-edu"
        }
      }
    }
}
actions {
  id: "generate_image"
  description: "Assemble prompt and generate infographic image"
  trigger_phrases: "generate infographic"
  trigger_phrases: "create infographic"
  trigger_phrases: "make infographic"
    rules {
      text: "Load layout definition from references/layouts/<layout>.md and style from references/styles/<style>.md"
      priority: HIGH
    }
    rules {
      text: "Combine layout + style + base template + structured content + confirmed language"
      priority: HIGH
    }
    rules {
      text: "Aspect ratio: landscape (16:9), portrait (9:16), square (1:1) — map to image_generate format"
      priority: NORMAL
    }
    data {
      key: "aspect_ratios"
      map_value {
        entries {
          key: "landscape"
          string_value: "16:9"
        }
        entries {
          key: "portrait"
          string_value: "9:16"
        }
        entries {
          key: "square"
          string_value: "1:1"
        }
      }
    }
}

guardrails {
  text: "Data integrity is paramount — never summarize or alter source statistics"
  scope: ALWAYS
}

guardrails {
  text: "Strip secrets from all output files"
  scope: ALWAYS
}

guardrails {
  text: "Style consistency — apply one style across entire infographic"
  scope: ALWAYS
}

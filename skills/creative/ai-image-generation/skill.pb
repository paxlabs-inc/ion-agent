meta {
  name: "ai-image-generation"
  version: "1.0.0"
  summary: "Generate brand-consistent images with AI providers — reference analysis, palette extraction, prompt engineering"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "image generation"
  keywords: "AI image"
  keywords: "brand palette"
  keywords: "xAI Grok"
  keywords: "FAL"
  keywords: "prompt engineering"
  intents: "generate_image"
  intents: "analyze_reference"
  intents: "extract_palette"
  patterns: "(generate|create|make) .*(image|illustration|picture)"
  patterns: "(brand|color) .*(palette|extraction)"
  patterns: "(reference|style) .*(image|analysis)"
}

requires {
  env_any: "XAI_API_KEY"
  env_any: "FAL_KEY"
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: false
  }
  binaries: "ffprobe"
  binaries: "ffmpeg"
}

provides {
  capabilities: "image_generation"
  capabilities: "vision_analysis"
  capabilities: "brand_palette_extraction"
  capabilities: "style_reference"
  output_types: ".png"
  output_types: ".jpg"
}

actions {
  id: "analyze_reference"
  description: "Examine reference images before generating — extract style DNA"
  trigger_phrases: "analyze reference"
  trigger_phrases: "extract style"
  trigger_phrases: "examine image"
    rules {
      text: "ALWAYS examine reference images before generating — never infer style from text alone"
      priority: CRITICAL
    }
    rules {
      text: "If vision_analyze tool available, use it; otherwise fall back to xAI API via execute_code"
      priority: HIGH
    }
    rules {
      text: "Use grok-4.5 for vision — do NOT use grok-2-vision (unavailable)"
      priority: HIGH
    }
    rules {
      text: "Describe visual style: colors, composition, typography, textures, lighting, materials, mood"
      priority: NORMAL
    }
    data {
      key: "vision_models"
      list_value {
        items {
          string_value: "grok-4.5"
        }
        items {
          string_value: "grok-4.3"
        }
      }
    }
    data {
      key: "banned_vision_model"
      string_value: "grok-2-vision"
    }
}
actions {
  id: "extract_palette"
  description: "Identify actual brand colors before generating"
  trigger_phrases: "extract palette"
  trigger_phrases: "find brand colors"
  trigger_phrases: "get color palette"
    rules {
      text: "NEVER use generic template colors or guess — fetch actual brand token file"
      priority: CRITICAL
    }
    rules {
      text: "Priority: brand kit repo → design tokens files → ask user for hex codes"
      priority: HIGH
    }
    rules {
      text: "Search for *tokens*, *colors*, *palette* files in the project"
      priority: NORMAL
    }
    data {
      key: "priority_order"
      list_value {
        items {
          string_value: "brand kit repo (e.g. GitHub raw URL)"
        }
        items {
          string_value: "design tokens files in project"
        }
        items {
          string_value: "ask user for hex codes"
        }
      }
    }
}
actions {
  id: "build_prompt"
  description: "Structure image generation prompts with style, subject, colors, composition"
  trigger_phrases: "build prompt"
  trigger_phrases: "write prompt"
  trigger_phrases: "craft prompt"
    rules {
      text: "Structure: art style/technique first, then subject, exact colors, composition, materials, lighting, exclusions"
      priority: HIGH
    }
    rules {
      text: "Include explicit exclusions ('No gradients, no 3D' when appropriate)"
      priority: HIGH
    }
    rules {
      text: "Use hex codes from brand palette for exact colors"
      priority: NORMAL
    }
    data {
      key: "prompt_structure"
      list_value {
        items {
          string_value: "art style/technique"
        }
        items {
          string_value: "subject/content"
        }
        items {
          string_value: "exact colors (hex)"
        }
        items {
          string_value: "composition notes"
        }
        items {
          string_value: "materials/textures"
        }
        items {
          string_value: "lighting"
        }
        items {
          string_value: "explicit exclusions"
        }
      }
    }
}
actions {
  id: "generate"
  description: "Generate images using image_generate tool"
  trigger_phrases: "generate image"
  trigger_phrases: "create image"
  trigger_phrases: "make image"
    rules {
      text: "Generate 3-5 variations per batch for user to choose from"
      priority: HIGH
    }
    rules {
      text: "Default aspect ratio: landscape (16:9) unless specified"
      priority: HIGH
    }
    rules {
      text: "All images in a batch should explore different subjects/compositions within same style"
      priority: NORMAL
    }
    data {
      key: "default_aspect"
      string_value: "landscape"
    }
    data {
      key: "variations_per_batch"
      int_value: 5
    }
}
actions {
  id: "video_style_analysis"
  description: "Extract frames from video and analyze for style reference"
  trigger_phrases: "analyze video style"
  trigger_phrases: "video reference"
  trigger_phrases: "extract video frames"
    rules {
      text: "Extract frames with ffmpeg before analyzing — never ask user to describe a video"
      priority: HIGH
    }
    rules {
      text: "Use 1fps for short videos, 0.5fps for longer; scale to 1024px"
      priority: NORMAL
    }
    data {
      key: "frame_extraction_command"
      string_value: "ffmpeg -y -i input.mp4 -vf \"fps=1,scale=1024:-1\" /tmp/ref_frame_%02d.png"
    }
}

guardrails {
  text: "Never guess brand colors from template files — always fetch actual brand token file"
  scope: ALWAYS
}

guardrails {
  text: "Never generate without analyzing references first when user provides them"
  scope: ALWAYS
}

guardrails {
  text: "API key redaction: use Python open() in execute_code to read .env values"
  scope: READ_OPS
}

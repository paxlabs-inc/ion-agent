meta {
  name: "image-generation"
  version: "1.0.0"
  summary: "Image generation and vision analysis via xAI (Grok Imagine) or FAL backends"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "image generation"
  keywords: "vision"
  keywords: "xai"
  keywords: "fal"
  keywords: "grok imagine"
  keywords: "ai art"
  keywords: "branding"
  keywords: "image analysis"
  intents: "generate_image"
  intents: "analyze_image"
  intents: "style_matching"
  intents: "vision_analysis"
  patterns: "(generate|create|make|produce) .*(image|picture|photo|illustration)"
  patterns: "(analyze|inspect|examine) .*(image|photo|picture)"
  patterns: "style.*(match|transfer|reference)"
  patterns: "(grok imagine|fal|image_gen)"
}

requires {
  env_all: "XAI_API_KEY"
  tools {
    name: "image_generate"
    required: true
  }
  tools {
    name: "terminal"
    required: false
  }
}

provides {
  capabilities: "image_generation"
  capabilities: "vision_analysis"
  capabilities: "style_matching"
  output_types: ".png"
  output_types: ".jpg"
  output_types: ".webp"
}

actions {
  id: "setup_providers"
  description: "Configure xAI or FAL as image generation backend"
  trigger_phrases: "set up image generation"
  trigger_phrases: "configure xai"
  trigger_phrases: "configure fal"
    rules {
      text: "xAI is the preferred backend. FAL is fallback only."
      priority: CRITICAL
    }
    rules {
      text: "Store XAI_API_KEY in config. Optionally set image_gen.provider and vision.provider."
      priority: HIGH
    }
    rules {
      text: "FAL auto-detects when FAL_KEY is set, or set image_gen.provider=fal explicitly."
      priority: HIGH
    }
    data {
      key: "xai_setup"
      map_value {
        entries {
          key: "api_key"
          string_value: "ion config set XAI_API_KEY '<key>'"
        }
        entries {
          key: "provider"
          string_value: "ion config set image_gen.provider xai"
        }
        entries {
          key: "vision"
          string_value: "ion config set vision.provider xai"
        }
      }
    }
    data {
      key: "fal_setup"
      map_value {
        entries {
          key: "api_key"
          string_value: "ion config set FAL_KEY '<key>'"
        }
        entries {
          key: "provider"
          string_value: "ion config set image_gen.provider fal"
        }
      }
    }
}
actions {
  id: "generate_image"
  description: "Generate images using the image_generate tool"
  trigger_phrases: "generate an image"
  trigger_phrases: "create a picture"
  trigger_phrases: "make an illustration"
  trigger_phrases: "image of"
    rules {
      text: "Use the image_generate tool directly. Parameters: prompt, aspect_ratio, image_url, reference_image_urls."
      priority: CRITICAL
    }
    rules {
      text: "aspect_ratio options: landscape (16:9), square (1:1), portrait (16:9 tall)."
      priority: HIGH
    }
    rules {
      text: "image_url enables image-to-image mode. reference_image_urls (up to 2) for style/composition references."
      priority: HIGH
    }
    rules {
      text: "xAI produces public URLs that persist (storage fees apply). Disable with image_gen.xai.storage.enabled=false."
      priority: HIGH
    }
    rules {
      text: "Up to 5 concurrent image_generate calls work well for batch generation."
      priority: NORMAL
    }
    data {
      key: "aspect_ratios"
      list_value {
        items {
          string_value: "landscape (16:9)"
        }
        items {
          string_value: "square (1:1)"
        }
        items {
          string_value: "portrait (16:9 tall)"
        }
      }
    }
    data {
      key: "max_reference_images"
      int_value: 2
    }
    data {
      key: "max_concurrent"
      int_value: 5
    }
}
actions {
  id: "vision_analysis"
  description: "Analyze images via xAI vision API or vision_analyze tool"
  trigger_phrases: "analyze this image"
  trigger_phrases: "describe this picture"
  trigger_phrases: "what's in this image"
  trigger_phrases: "vision analysis"
    rules {
      text: "Use vision_analyze tool if available. Fallback: call xAI chat completions API directly via execute_code."
      priority: CRITICAL
    }
    rules {
      text: "No grok-2-vision model exists. Use grok-4.5 or grok-4.3 for vision tasks."
      priority: CRITICAL
    }
    rules {
      text: "Encode images as base64 and pass with detail='high' for best results."
      priority: HIGH
    }
    rules {
      text: "xAI vision endpoint: POST https://api.x.ai/v1/chat/completions with model grok-4.5."
      priority: HIGH
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
      key: "generation_models"
      list_value {
        items {
          string_value: "grok-imagine-image"
        }
        items {
          string_value: "grok-imagine-image-quality"
        }
      }
    }
    data {
      key: "api_endpoint"
      string_value: "https://api.x.ai/v1/chat/completions"
    }
    examples {
      label: "vision analysis via execute_code fallback"
      language: "python"
      code: "import base64, json, os, urllib.request\napi_key = \"\"\nwith open(\"/data/.ion/.env\") as f:\n    for line in f:\n        if line.startswith(\"XAI_API_KEY=\"):\n            api_key = line.strip().split(\"=\", 1)[1]; break\nwith open(\"/path/to/image.jpg\", \"rb\") as f:\n    b64 = base64.b64encode(f.read()).decode()\npayload = json.dumps({\"model\": \"grok-4.5\", \"messages\": [{\"role\": \"user\", \"content\": [\n    {\"type\": \"image_url\", \"image_url\": {\"url\": f\"data:image/jpeg;base64,{b64}\", \"detail\": \"high\"}},\n    {\"type\": \"text\", \"text\": \"Describe the visual style in detail.\"}\n]}], \"max_tokens\": 600})\nreq = urllib.request.Request(\"https://api.x.ai/v1/chat/completions\", data=payload.encode(),\n    headers={\"Content-Type\": \"application/json\", \"Authorization\": f\"Bearer {api_key}\"})\nresp = urllib.request.urlopen(req, timeout=60)\nprint(json.loads(resp.read())[\"choices\"][0][\"message\"][\"content\"])"
    }
}
actions {
  id: "style_matched_generation"
  description: "Generate images matching the style of reference images"
  trigger_phrases: "match this style"
  trigger_phrases: "style-matched generation"
  trigger_phrases: "generate in this style"
    rules {
      text: "Workflow: 1) Analyze each reference via vision API, 2) Synthesize style DNA, 3) Generate with encoded style, 4) Present with style notes."
      priority: CRITICAL
    }
    rules {
      text: "Style DNA components: color palette, composition, materials/textures, lighting, typography, mood/atmosphere."
      priority: HIGH
    }
    rules {
      text: "For premium tech aesthetic: define background (deep black void), materials (frosted glass), lighting (dramatic rim), composition (generous negative space), quality (ultra-clean 8K)."
      priority: HIGH
    }
    rules {
      text: "Include 'no text' in prompts to prevent unwanted typography artifacts."
      priority: NORMAL
    }
    data {
      key: "style_dna_components"
      list_value {
        items {
          string_value: "color_palette"
        }
        items {
          string_value: "composition"
        }
        items {
          string_value: "materials_textures"
        }
        items {
          string_value: "lighting"
        }
        items {
          string_value: "typography"
        }
        items {
          string_value: "mood_atmosphere"
        }
      }
    }
    data {
      key: "premium_tech_defaults"
      map_value {
        entries {
          key: "background"
          string_value: "deep black void, pure matte black"
        }
        entries {
          key: "materials"
          string_value: "frosted glass, holographic, subsurface scattering"
        }
        entries {
          key: "lighting"
          string_value: "dramatic rim lighting, soft bloom, volumetric glow"
        }
        entries {
          key: "composition"
          string_value: "generous negative space, asymmetric balance"
        }
        entries {
          key: "quality"
          string_value: "ultra-clean, photorealistic, 8K, editorial quality"
        }
      }
    }
}

guardrails {
  text: "No grok-2-vision model exists — use grok-4.5 or grok-4.3 for vision tasks"
  scope: ALWAYS
}

guardrails {
  text: "xAI public URLs persist and incur storage fees — warn user or disable storage"
  scope: ALWAYS
}

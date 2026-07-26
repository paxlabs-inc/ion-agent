meta {
  name: "ascii-video"
  version: "1.0.0"
  summary: "Convert video/audio/images to colored ASCII MP4/GIF with production pipeline"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "ASCII video"
  keywords: "text art video"
  keywords: "terminal video"
  keywords: "character art animation"
  keywords: "retro visualization"
  intents: "create_ascii_video"
  intents: "video_to_ascii"
  intents: "audio_visualizer"
  intents: "generative_ascii"
  patterns: "(create|make|generate) .*(ASCII|text) .*(video|animation|visualizer)"
  patterns: "(convert|transform) .*(video|audio) .*(ASCII|text)"
  patterns: "(audio|music) .*(visualizer|reactive) .*(ASCII|text)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: true
  }
  binaries: "python3"
  binaries: "ffmpeg"
  binaries: "ffprobe"
}

provides {
  capabilities: "ascii_video"
  capabilities: "audio_reactive_visuals"
  capabilities: "generative_ascii_animation"
  capabilities: "video_to_ascii"
  output_types: ".mp4"
  output_types: ".gif"
}

actions {
  id: "creative_vision"
  description: "Define the creative concept before any code"
  trigger_phrases: "plan ASCII video"
  trigger_phrases: "design concept"
  trigger_phrases: "creative direction"
    rules {
      text: "Before writing code, articulate: mood, visual story, color world, character texture, what makes THIS different"
      priority: CRITICAL
    }
    rules {
      text: "First-render excellence is non-negotiable — output must be visually striking without revision"
      priority: CRITICAL
    }
    rules {
      text: "Cohesive aesthetic over technical correctness — all scenes must share visual language"
      priority: HIGH
    }
    rules {
      text: "Dense, layered, considered — never flat black backgrounds, always multi-grid composition"
      priority: HIGH
    }
    rules {
      text: "Be proactively creative — include at least one visual moment user didn't ask for but will appreciate"
      priority: NORMAL
    }
    data {
      key: "aesthetic_dimensions"
      list_value {
        items {
          string_value: "character_palette"
        }
        items {
          string_value: "color_strategy"
        }
        items {
          string_value: "background_texture"
        }
        items {
          string_value: "primary_effects"
        }
        items {
          string_value: "particles"
        }
        items {
          string_value: "shader_mood"
        }
        items {
          string_value: "grid_density"
        }
        items {
          string_value: "coordinate_space"
        }
        items {
          string_value: "feedback"
        }
        items {
          string_value: "masking"
        }
        items {
          string_value: "transitions"
        }
      }
    }
}
actions {
  id: "build_script"
  description: "Build single self-contained Python script following the 6-stage pipeline"
  trigger_phrases: "build script"
  trigger_phrases: "write script"
  trigger_phrases: "create pipeline"
    rules {
      text: "Single self-contained Python script per project — no GPU required"
      priority: CRITICAL
    }
    rules {
      text: "Pipeline: INPUT → ANALYZE → SCENE_FN → TONEMAP → SHADE → ENCODE"
      priority: CRITICAL
    }
    rules {
      text: "Never use canvas * N multipliers for brightness — use adaptive tonemap()"
      priority: HIGH
    }
    rules {
      text: "Per-scene variation required: different background, palette, color strategy, shader intensity"
      priority: HIGH
    }
    rules {
      text: "Invent at least one custom element per project: palette, effect, color, particle, or transition"
      priority: HIGH
    }
    rules {
      text: "Target: ~100-200ms/frame total; character render is the bottleneck at 80-150ms"
      priority: NORMAL
    }
    data {
      key: "pipeline_stages"
      list_value {
        items {
          string_value: "INPUT — load/decode source material"
        }
        items {
          string_value: "ANALYZE — extract per-frame features"
        }
        items {
          string_value: "SCENE_FN — render to pixel canvas (uint8 H,W,3)"
        }
        items {
          string_value: "TONEMAP — percentile-based adaptive brightness"
        }
        items {
          string_value: "SHADE — ShaderChain + FeedbackBuffer post-processing"
        }
        items {
          string_value: "ENCODE — pipe raw RGB to ffmpeg for H.264/GIF"
        }
      }
    }
    data {
      key: "stack"
      map_value {
        entries {
          key: "core"
          string_value: "Python 3.10+, NumPy"
        }
        entries {
          key: "signal"
          string_value: "SciPy (FFT, peak detection)"
        }
        entries {
          key: "imaging"
          string_value: "Pillow (PIL)"
        }
        entries {
          key: "video_io"
          string_value: "ffmpeg (CLI)"
        }
        entries {
          key: "parallel"
          string_value: "concurrent.futures"
        }
      }
    }
    data {
      key: "tonemap_function"
      string_value: "def tonemap(canvas, gamma=0.75): f = canvas.astype(np.float32); lo, hi = np.percentile(f[::4, ::4], [1, 99.5]); return (np.clip((f - lo) / (max(hi - lo, 10)), 0, 1) ** gamma * 255).astype(np.uint8)"
    }
}
actions {
  id: "quality_verification"
  description: "Verify output quality before delivery"
  trigger_phrases: "verify quality"
  trigger_phrases: "check output"
  trigger_phrases: "test frames"
    rules {
      text: "Test frames first: render single frames at key timestamps before full render"
      priority: HIGH
    }
    rules {
      text: "Brightness check: canvas.mean() > 8 for all ASCII content"
      priority: HIGH
    }
    rules {
      text: "Visual coherence: do all scenes feel like they belong to the same video?"
      priority: NORMAL
    }
}

guardrails {
  text: "Single self-contained Python script — no external frameworks"
  scope: ALWAYS
}

guardrails {
  text: "Verify brightness and visual coherence before delivering"
  scope: ALWAYS
}

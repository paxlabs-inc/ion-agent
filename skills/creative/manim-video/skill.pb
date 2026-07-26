meta {
  name: "manim-video"
  version: "1.0.0"
  summary: "Manim CE animations — 3Blue1Brown-style math/algo explainer videos"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "manim"
  keywords: "animation"
  keywords: "math video"
  keywords: "3blue1brown"
  keywords: "explainer video"
  keywords: "algorithm visualization"
  keywords: "equation derivation"
  intents: "create_animation"
  intents: "math_explanation"
  intents: "algorithm_walkthrough"
  intents: "concept_visualization"
  patterns: "(create|make|build) .*(animation|video|explainer)"
  patterns: "(animate|visualize) .*(math|algorithm|equation|concept)"
  patterns: "3blue1brown"
  patterns: "manim"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "terminal"
    required: true
  }
  binaries: "python3"
  binaries: "manim"
  binaries: "ffmpeg"
  binaries: "latex"
}

provides {
  capabilities: "math_animations"
  capabilities: "algorithm_visualizations"
  capabilities: "equation_derivations"
  capabilities: "data_stories"
  output_types: ".mp4"
  output_types: ".png"
}

actions {
  id: "plan_animation"
  description: "Create narrative plan before writing any code"
  trigger_phrases: "plan animation"
  trigger_phrases: "plan video"
  trigger_phrases: "design the narrative"
    rules {
      text: "Before writing code, create plan.md with: narrative arc, scene list, visual elements, color palette, voiceover script."
      priority: CRITICAL
    }
    rules {
      text: "Articulate the narrative arc first: what misconception, what 'aha moment', what visual journey from confusion to clarity."
      priority: CRITICAL
    }
    rules {
      text: "Geometry precedes algebra — display the shape first, then the equation."
      priority: HIGH
    }
    rules {
      text: "First-render excellence is mandatory — output must be visually clear without revision rounds."
      priority: HIGH
    }
}
actions {
  id: "write_scene"
  description: "Write Manim scene code — one class per scene"
  trigger_phrases: "write manim scene"
  trigger_phrases: "code the animation"
  trigger_phrases: "create scene"
    rules {
      text: "One Python script per project. One class per scene. Every scene independently renderable."
      priority: CRITICAL
    }
    rules {
      text: "Monospace fonts required for all text — Manim's Pango renderer generates broken kerning with proportional fonts."
      priority: CRITICAL
    }
    rules {
      text: "Always use raw strings for LaTeX: MathTex(r'\\frac{1}{2}') not MathTex('\\frac{1}{2}')."
      priority: CRITICAL
    }
    rules {
      text: "Opacity layering: primary 1.0, contextual 0.4, structural (axes/grids) 0.15."
      priority: HIGH
    }
    rules {
      text: "Every animation needs self.wait() after it — viewer needs time to absorb."
      priority: HIGH
    }
    rules {
      text: "Shared color constants at file top for cross-scene consistency."
      priority: HIGH
    }
    rules {
      text: "FadeOut all mobjects at scene end: self.play(FadeOut(Group(*self.mobjects)))."
      priority: HIGH
    }
    rules {
      text: "Minimum font_size=18 for readability."
      priority: HIGH
    }
    rules {
      text: "Per-scene variation: different dominant color, different layout, different animation entry, different visual weight."
      priority: NORMAL
    }
    data {
      key: "color_palettes"
      map_value {
        entries {
          key: "classic_3b1b"
          string_value: "BG=#1C1C1C, PRIMARY=#58C4DD, SECONDARY=#83C167, ACCENT=#FFFF00"
        }
        entries {
          key: "warm_academic"
          string_value: "BG=#2D2B55, PRIMARY=#FF6B6B, SECONDARY=#FFD93D, ACCENT=#6BCB77"
        }
        entries {
          key: "neon_tech"
          string_value: "BG=#0A0A0A, PRIMARY=#00F5FF, SECONDARY=#FF00FF, ACCENT=#39FF14"
        }
        entries {
          key: "monochrome"
          string_value: "BG=#1A1A2E, PRIMARY=#EAEAEA, SECONDARY=#888888, ACCENT=#FFFFFF"
        }
      }
    }
    data {
      key: "typography_scale"
      map_value {
        entries {
          key: "title"
          int_value: 48
        }
        entries {
          key: "heading"
          int_value: 36
        }
        entries {
          key: "body"
          int_value: 30
        }
        entries {
          key: "label"
          int_value: 24
        }
        entries {
          key: "caption"
          int_value: 20
        }
      }
    }
    data {
      key: "animation_speeds"
      map_value {
        entries {
          key: "title_appear"
          string_value: "1.5s run, 1.0s wait"
        }
        entries {
          key: "equation_reveal"
          string_value: "2.0s run, 2.0s wait"
        }
        entries {
          key: "transform_morph"
          string_value: "1.5s run, 1.5s wait"
        }
        entries {
          key: "aha_moment"
          string_value: "2.5s run, 3.0s wait"
        }
      }
    }
    examples {
      label: "basic scene with color constants"
      language: "python"
      code: "from manim import *\nBG = \"#1C1C1C\"; PRIMARY = \"#58C4DD\"; MONO = \"Menlo\"\nclass Scene1_Intro(Scene):\n    def construct(self):\n        self.camera.background_color = BG\n        title = Text(\"Why Does This Work?\", font_size=48, color=PRIMARY, weight=BOLD, font=MONO)\n        self.play(Write(title), run_time=1.5)\n        self.wait(1.0)\n        self.play(FadeOut(title), run_time=0.5)"
    }
}
actions {
  id: "render_and_stitch"
  description: "Render scenes and stitch into final video"
  trigger_phrases: "render animation"
  trigger_phrases: "stitch scenes"
  trigger_phrases: "produce video"
    rules {
      text: "Draft: manim -ql script.py SceneName. Production: manim -qh script.py SceneName."
      priority: HIGH
    }
    rules {
      text: "Stitch with ffmpeg -f concat -safe 0 -i concat.txt -c copy final.mp4."
      priority: HIGH
    }
    rules {
      text: "Always iterate at -ql (854x480 15fps). Only render -qh (1920x1080 60fps) for final."
      priority: NORMAL
    }
    data {
      key: "quality_presets"
      map_value {
        entries {
          key: "draft"
          string_value: "-ql 854x480 15fps 5-15s/scene"
        }
        entries {
          key: "medium"
          string_value: "-qm 1280x720 30fps 15-60s/scene"
        }
        entries {
          key: "production"
          string_value: "-qh 1920x1080 60fps 30-120s/scene"
        }
      }
    }
}

guardrails {
  text: "Never use non-monospace fonts for text — Manim Pango renderer breaks kerning"
  scope: ALWAYS
}

guardrails {
  text: "Always use raw strings (r-prefix) for LaTeX in MathTex"
  scope: ALWAYS
}

guardrails {
  text: "buff >= 0.5 for edge text positioning — never less"
  scope: ALWAYS
}

meta {
  name: "p5js"
  version: "1.0.0"
  summary: "p5.js sketches — generative art, shaders, interactive visualizations, 3D scenes"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "p5js"
  keywords: "p5.js"
  keywords: "creative coding"
  keywords: "generative art"
  keywords: "canvas animation"
  keywords: "interactive visualization"
  keywords: "shader"
  keywords: "webgl"
  intents: "create_sketch"
  intents: "generative_art"
  intents: "data_visualization"
  intents: "interactive_experience"
  intents: "shader_effect"
  patterns: "(create|make|build) .*(p5|sketch|generative|canvas)"
  patterns: "p5\\.js"
  patterns: "(interactive|generative) .*(art|visualization|animation)"
  patterns: "(shader|webgl|3d) .*(scene|effect)"
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
  capabilities: "generative_art"
  capabilities: "interactive_visualizations"
  capabilities: "3d_scenes"
  capabilities: "shader_effects"
  capabilities: "data_viz"
  output_types: ".html"
  output_types: ".png"
  output_types: ".gif"
  output_types: ".mp4"
  output_types: ".svg"
}

actions {
  id: "creative_vision"
  description: "Articulate creative concept before coding"
  trigger_phrases: "design the concept"
  trigger_phrases: "plan the sketch"
  trigger_phrases: "creative direction"
    rules {
      text: "Before code: articulate mood, visual story, color world, shape language, motion vocabulary, what makes this unique."
      priority: CRITICAL
    }
    rules {
      text: "First-render excellence is mandatory — must be visually striking on first load."
      priority: HIGH
    }
    rules {
      text: "Be proactively creative — include at least one visual detail the user didn't ask for but will appreciate."
      priority: HIGH
    }
    rules {
      text: "Dense, layered, considered — every frame should reward viewing. Never flat white backgrounds."
      priority: NORMAL
    }
}
actions {
  id: "build_sketch"
  description: "Build a p5.js sketch as single self-contained HTML file"
  trigger_phrases: "build p5 sketch"
  trigger_phrases: "create p5js sketch"
  trigger_phrases: "code the visualization"
    rules {
      text: "Single self-contained HTML file. No build step. Inline <style> and <script>."
      priority: CRITICAL
    }
    rules {
      text: "Disable FES (Friendly Error System) for performance: p5.disableFriendlyErrors = true BEFORE setup()."
      priority: CRITICAL
    }
    rules {
      text: "Always seed randomness: randomSeed(CONFIG.seed) + noiseSeed(CONFIG.seed) for reproducibility."
      priority: CRITICAL
    }
    rules {
      text: "Use colorMode(HSB, 360, 100, 100, 100) for intuitive color control."
      priority: HIGH
    }
    rules {
      text: "Structure: globals → preload() → setup() → draw() → helpers → classes → event handlers."
      priority: HIGH
    }
    rules {
      text: "Never hardcode raw RGB values — define a palette object, derive variations procedurally."
      priority: HIGH
    }
    rules {
      text: "Custom background treatment always — never plain background(0) or background(255)."
      priority: HIGH
    }
    rules {
      text: "p5.js 1.x (1.11.3) is default. Use 2.x only when features required (async setup, OKLCH color, splineVertex)."
      priority: NORMAL
    }
    rules {
      text: "For hot loops use Math.* instead of p5 wrappers — measurably faster."
      priority: NORMAL
    }
    data {
      key: "version_default"
      string_value: "1.11.3"
    }
    data {
      key: "cdn_url"
      string_value: "https://cdnjs.cloudflare.com/ajax/libs/p5.js/1.11.3/p5.min.js"
    }
    data {
      key: "modes"
      list_value {
        items {
          string_value: "generative_art"
        }
        items {
          string_value: "data_visualization"
        }
        items {
          string_value: "interactive_experience"
        }
        items {
          string_value: "animation_motion_graphics"
        }
        items {
          string_value: "3d_scene"
        }
        items {
          string_value: "image_processing"
        }
        items {
          string_value: "audio_reactive"
        }
      }
    }
    data {
      key: "performance_targets"
      map_value {
        entries {
          key: "frame_rate_interactive"
          string_value: "60fps sustained"
        }
        entries {
          key: "frame_rate_animated"
          string_value: "30fps minimum"
        }
        entries {
          key: "particles_p2d"
          string_value: "5000-10000 at 60fps"
        }
        entries {
          key: "particles_pixel"
          string_value: "50000-100000 at 60fps"
        }
        entries {
          key: "max_resolution"
          string_value: "3840x2160 export, 1920x1080 interactive"
        }
      }
    }
    examples {
      label: "minimal sketch structure"
      language: "html"
      code: "<!DOCTYPE html><html><head>\n<script>p5.disableFriendlyErrors = true;</script>\n<script src=\"https://cdnjs.cloudflare.com/ajax/libs/p5.js/1.11.3/p5.min.js\"></script>\n<style>html,body{margin:0;padding:0;overflow:hidden;}canvas{display:block;}</style>\n</head><body><script>\nconst CONFIG = { seed: 42 };\nconst PALETTE = { bg: '#0a0a0f', primary: '#e8d5b7' };\nfunction setup() { createCanvas(1920,1080); randomSeed(CONFIG.seed); noiseSeed(CONFIG.seed); colorMode(HSB,360,100,100,100); }\nfunction draw() { background(PALETTE.bg); }\nfunction keyPressed() { if(key==='s') saveCanvas('output','png'); }\n</script></body></html>"
    }
}
actions {
  id: "export_sketch"
  description: "Export p5.js sketch as PNG, GIF, MP4, or SVG"
  trigger_phrases: "export sketch"
  trigger_phrases: "save as png"
  trigger_phrases: "render video"
  trigger_phrases: "capture frames"
    rules {
      text: "PNG: saveCanvas('output','png') on key 's'. GIF: saveGif('output',5) on key 'g'."
      priority: HIGH
    }
    rules {
      text: "MP4: Puppeteer frame capture + ffmpeg. SVG: createCanvas(w,h,SVG) with p5.js-svg."
      priority: HIGH
    }
    rules {
      text: "Headless export requires noLoop() in setup + window._p5Ready = true signal."
      priority: HIGH
    }
    rules {
      text: "For multi-scene videos: one HTML per scene, render independently, stitch with ffmpeg -f concat."
      priority: NORMAL
    }
    data {
      key: "export_methods"
      map_value {
        entries {
          key: "png"
          string_value: "saveCanvas('output','png')"
        }
        entries {
          key: "gif"
          string_value: "saveGif('output',5)"
        }
        entries {
          key: "frames"
          string_value: "saveFrames('frame','png',10,30) then ffmpeg"
        }
        entries {
          key: "mp4"
          string_value: "Puppeteer + ffmpeg via scripts/render.sh"
        }
        entries {
          key: "svg"
          string_value: "createCanvas(w,h,SVG) + save('output.svg')"
        }
      }
    }
}

guardrails {
  text: "Disable FES in every production sketch — adds up to 10x overhead"
  scope: ALWAYS
}

guardrails {
  text: "Always seed randomness for reproducible generative art"
  scope: ALWAYS
}

guardrails {
  text: "Never console.log() inside draw() — never manipulate DOM in draw()"
  scope: ALWAYS
}

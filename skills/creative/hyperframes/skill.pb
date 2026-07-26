meta {
  name: "hyperframes"
  version: "1.0.0"
  summary: "HTML-driven video compositions — animated titles, overlays, captions, audio-reactive graphics"
  author: "HeyGen"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "hyperframes"
  keywords: "HTML video"
  keywords: "video composition"
  keywords: "animated title"
  keywords: "video overlay"
  keywords: "caption video"
  intents: "create_html_video"
  intents: "render_composition"
  intents: "build_video_overlay"
  patterns: "(create|make|build) .*(HTML|animated) .*(video|composition|title|overlay)"
  patterns: "(render|export) .*(HTML|composition) .*(MP4|WebM|video)"
  patterns: "hyperframes"
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
  binaries: "node"
  binaries: "npx"
  binaries: "ffmpeg"
}

provides {
  capabilities: "html_video_composition"
  capabilities: "video_rendering"
  capabilities: "animated_titles"
  capabilities: "caption_overlay"
  output_types: ".mp4"
  output_types: ".webm"
}

actions {
  id: "project_setup"
  description: "Initialize HyperFrames project"
  trigger_phrases: "init hyperframes"
  trigger_phrases: "new project"
  trigger_phrases: "set up hyperframes"
    rules {
      text: "Requires Node.js 22+ and ffmpeg installed"
      priority: HIGH
    }
    rules {
      text: "Init: npx hyperframes init my-video --non-interactive --example blank"
      priority: HIGH
    }
    rules {
      text: "Generates: index.html, meta.json, package.json, hyperframes.json"
      priority: NORMAL
    }
    data {
      key: "init_command"
      string_value: "npx hyperframes init my-video --non-interactive --example blank"
    }
    data {
      key: "prereq_chrome_deps"
      string_value: "apt install -y unzip libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 libcups2 libdrm2 libxkbcommon0 libatspi2.0-0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libpango-1.0-0 libcairo2 libasound2"
    }
}
actions {
  id: "create_composition"
  description: "Build HTML composition with timed clips and GSAP animations"
  trigger_phrases: "create composition"
  trigger_phrases: "build composition"
  trigger_phrases: "design composition"
    rules {
      text: "Root element must have data-composition-id, data-width, data-height"
      priority: CRITICAL
    }
    rules {
      text: "Every timed element needs class='clip' AND data-start, data-duration, data-track-index"
      priority: CRITICAL
    }
    rules {
      text: "GSAP timeline needs { paused: true } and registered on window.__timelines"
      priority: CRITICAL
    }
    rules {
      text: "Sub-composition files must NOT have data-composition-id — triggers 'multiple root' error"
      priority: HIGH
    }
    rules {
      text: "No overlapping clips on same track — adjust timing or use different data-track-index"
      priority: HIGH
    }
    rules {
      text: "Deterministic only — no Date.now(), Math.random(), or network fetches"
      priority: HIGH
    }
    rules {
      text: "Self-contained: inline styles, inline scripts, CDN-loaded libraries"
      priority: NORMAL
    }
    examples {
      label: "GSAP timeline registration"
      language: "javascript"
      code: "const tl = gsap.timeline({ paused: true });\ntl.from(\"#title\", { opacity: 0, y: 40, duration: 0.8, ease: \"power3.out\" }, 0.6);\nwindow.__timelines = window.__timelines || {};\nwindow.__timelines[\"your-composition-id\"] = tl;"
    }
}
actions {
  id: "render_video"
  description: "Render composition to MP4/WebM"
  trigger_phrases: "render video"
  trigger_phrases: "export video"
  trigger_phrases: "render composition"
    rules {
      text: "Render: npx hyperframes render --output out.mp4"
      priority: HIGH
    }
    rules {
      text: "Specific file: npx hyperframes render --composition foo.html --output foo.mp4"
      priority: HIGH
    }
    rules {
      text: "--composition takes path relative to project directory — there is NO --input flag"
      priority: HIGH
    }
    rules {
      text: "Keep compositions under 10s for rapid iteration; merge with ffmpeg afterward"
      priority: NORMAL
    }
    data {
      key: "render_command"
      string_value: "npx hyperframes render --output out.mp4"
    }
    data {
      key: "render_specific"
      string_value: "npx hyperframes render --composition foo.html --output foo.mp4"
    }
    data {
      key: "ffmpeg_concat"
      list_value {
        items {
          string_value: "ffmpeg -y -f concat -safe 0 -i concat.txt -c copy combined.mp4"
        }
      }
    }
}
actions {
  id: "validate"
  description: "Lint and validate project before rendering"
  trigger_phrases: "lint hyperframes"
  trigger_phrases: "validate project"
  trigger_phrases: "check composition"
    rules {
      text: "Lint: npx hyperframes lint — pass DIRECTORY, not file"
      priority: HIGH
    }
    rules {
      text: "Preview: npx hyperframes preview before committing to full render"
      priority: NORMAL
    }
    data {
      key: "lint_command"
      string_value: "npx hyperframes lint"
    }
    data {
      key: "preview_command"
      string_value: "npx hyperframes preview"
    }
}

guardrails {
  text: "Sub-composition files must NOT have data-composition-id"
  scope: ALWAYS
}

guardrails {
  text: "No overlapping clips on the same track"
  scope: ALWAYS
}

guardrails {
  text: "All content must be deterministic — no Date.now(), Math.random(), or network fetches"
  scope: ALWAYS
}

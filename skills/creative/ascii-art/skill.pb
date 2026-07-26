meta {
  name: "ascii-art"
  version: "4.0.0"
  summary: "ASCII art toolkit — pyfiglet, cowsay, boxes, image-to-ascii, QR codes, weather art"
  author: "0xbyt4, Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "ASCII art"
  keywords: "text banner"
  keywords: "cowsay"
  keywords: "boxes"
  keywords: "pyfiglet"
  keywords: "figlet"
  keywords: "image to ASCII"
  keywords: "QR code"
  intents: "create_ascii_art"
  intents: "text_banner"
  intents: "ascii_border"
  intents: "image_to_ascii"
  patterns: "(create|make|generate) .*(ASCII|text) .*(art|banner|border)"
  patterns: "(cowsay|boxes|pyfiglet|figlet)"
  patterns: "(convert|transform) .*(image|picture) .*(ASCII|text)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "write_file"
    required: false
  }
  binaries: "python3"
  binaries: "curl"
}

provides {
  capabilities: "ascii_art"
  capabilities: "text_banners"
  capabilities: "message_art"
  capabilities: "decorative_borders"
  capabilities: "image_to_ascii"
  output_types: ".txt"
}

actions {
  id: "text_banner"
  description: "Transform text into large ASCII art banners"
  trigger_phrases: "text banner"
  trigger_phrases: "ASCII banner"
  trigger_phrases: "big text"
    rules {
      text: "Use pyfiglet if installed, otherwise fall back to asciified API via curl"
      priority: HIGH
    }
    rules {
      text: "Preview 2-3 fonts and let user pick their favorite"
      priority: HIGH
    }
    rules {
      text: "Short text (1-8 chars) works best with doom/block fonts; long text with small/mini"
      priority: NORMAL
    }
    data {
      key: "pyfiglet_command"
      string_value: "python3 -m pyfiglet 'TEXT' -f slant"
    }
    data {
      key: "asciified_api"
      string_value: "https://asciified.thelicato.io/api/v2/ascii?text=Hello+World"
    }
    data {
      key: "recommended_fonts"
      map_value {
        entries {
          key: "clean_modern"
          string_value: "slant"
        }
        entries {
          key: "bold_blocky"
          string_value: "doom"
        }
        entries {
          key: "big_readable"
          string_value: "big"
        }
        entries {
          key: "classic_banner"
          string_value: "banner3"
        }
        entries {
          key: "compact"
          string_value: "small"
        }
        entries {
          key: "cyberpunk"
          string_value: "cyberlarge"
        }
      }
    }
}
actions {
  id: "cowsay_art"
  description: "Wrap text in speech bubbles with ASCII characters"
  trigger_phrases: "cowsay"
  trigger_phrases: "message art"
  trigger_phrases: "speech bubble"
    rules {
      text: "50+ characters available: default, tux, dragon, stegosaurus, vader, etc."
      priority: NORMAL
    }
    rules {
      text: "Eye modifiers: -b borg, -d dead, -g greedy, -p paranoid, -s stoned, -w wired"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "sudo apt install cowsay -y"
    }
    data {
      key: "characters"
      list_value {
        items {
          string_value: "default"
        }
        items {
          string_value: "tux"
        }
        items {
          string_value: "dragon"
        }
        items {
          string_value: "stegosaurus"
        }
        items {
          string_value: "vader"
        }
        items {
          string_value: "kitty"
        }
      }
    }
}
actions {
  id: "decorative_border"
  description: "Draw ornamental ASCII art borders/frames around text"
  trigger_phrases: "boxes"
  trigger_phrases: "decorative border"
  trigger_phrases: "frame text"
    rules {
      text: "70+ built-in designs: stone, parchment, cat, dog, diamonds, c-cmt, html-cmt"
      priority: NORMAL
    }
    rules {
      text: "Can combine with pyfiglet: python3 -m pyfiglet 'ION' -f slant | boxes -d stone"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "sudo apt install boxes -y"
    }
}
actions {
  id: "image_to_ascii"
  description: "Convert images (PNG, JPEG, GIF, WEBP) to ASCII art"
  trigger_phrases: "image to ASCII"
  trigger_phrases: "convert image to ASCII"
  trigger_phrases: "ASCII from image"
    rules {
      text: "Option A: ascii-image-converter (recommended, modern) — supports color, braille, dimensions"
      priority: HIGH
    }
    rules {
      text: "Option B: jp2a (lightweight, JPEG only)"
      priority: NORMAL
    }
    rules {
      text: "ascii-image-converter: sudo snap install ascii-image-converter"
      priority: NORMAL
    }
    data {
      key: "recommended_tool"
      string_value: "ascii-image-converter"
    }
    data {
      key: "commands"
      list_value {
        items {
          string_value: "ascii-image-converter image.png"
        }
        items {
          string_value: "ascii-image-converter image.png -C"
        }
        items {
          string_value: "ascii-image-converter image.png -d 60,30"
        }
      }
    }
}
actions {
  id: "search_pre_made"
  description: "Search curated ASCII art from the web"
  trigger_phrases: "find ASCII art"
  trigger_phrases: "search ASCII art"
  trigger_phrases: "pre-made ASCII"
    rules {
      text: "Source: ascii.co.uk/art/{subject} — fetch with curl, extract from <pre> tags"
      priority: HIGH
    }
    rules {
      text: "Subjects: animals (cat, dog, dragon), objects (rocket, guitar), nature (tree, moon), characters (skull, robot)"
      priority: NORMAL
    }
    rules {
      text: "Preserve artist signatures/initials — important etiquette"
      priority: NORMAL
    }
    data {
      key: "url_pattern"
      string_value: "https://ascii.co.uk/art/{subject}"
    }
}
actions {
  id: "llm_custom_art"
  description: "Generate custom ASCII art using Unicode character palette"
  trigger_phrases: "custom ASCII art"
  trigger_phrases: "generate ASCII art"
  trigger_phrases: "create ASCII"
    rules {
      text: "Max width: 60 characters per line (terminal-safe)"
      priority: HIGH
    }
    rules {
      text: "Max height: 15 lines for banners, 25 for scenes"
      priority: HIGH
    }
    rules {
      text: "Monospace only — output must render correctly in fixed-width fonts"
      priority: NORMAL
    }
    data {
      key: "box_drawing_chars"
      string_value: "╔ ╗ ╚ ╝ ║ ═ ╠ ╣ ╦ ╩ ╬ ┌ ┐ └ ┘ │ ─ ├ ┤ ┬ ┴ ┼ ╭ ╮ ╰ ╯"
    }
    data {
      key: "block_elements"
      string_value: "░ ▒ ▓ █ ▄ ▀ ▌ ▐ ▖ ▗ ▘ ▝ ▚ ▞"
    }
    data {
      key: "geometric_symbols"
      string_value: "◆ ◇ ◈ ● ○ ◉ ■ □ ▲ △ ▼ ▽ ★ ☆ ✦ ✧ ◀ ▶ ◁ ▷ ⬡ ⬢ ⌂"
    }
}

guardrails {
  text: "Always offer multiple options — pyfiglet, cowsay, boxes, ascii.co.uk — pick best fit"
  scope: ALWAYS
}

related {
  name: "excalidraw"
}

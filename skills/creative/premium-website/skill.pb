meta {
  name: "premium-website"
  version: "1.0.0"
  summary: "Multi-page premium websites — shared CSS/JS architecture, brand assets, anti-slop design"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "premium website"
  keywords: "multi-page site"
  keywords: "corporate site"
  keywords: "company website"
  keywords: "professional website"
  keywords: "org website"
  intents: "build_website"
  intents: "create_corporate_site"
  intents: "multi_page_site"
  patterns: "(build|create|make) .*(website|site) .*(company|corporate|professional|multi-page)"
  patterns: "(proper|production) .*(website|site)"
  patterns: "multi-page"
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
}

provides {
  capabilities: "multi_page_websites"
  capabilities: "shared_design_systems"
  capabilities: "brand_asset_integration"
  output_types: ".html"
  output_types: ".css"
  output_types: ".js"
}

actions {
  id: "build_site_architecture"
  description: "Build multi-page site with shared CSS/JS design system"
  trigger_phrases: "build the site"
  trigger_phrases: "create the architecture"
  trigger_phrases: "set up the site structure"
    rules {
      text: "Directory structure: index.html (home), products.html, developers.html, blog.html, about.html, css/style.css, js/main.js, images/."
      priority: CRITICAL
    }
    rules {
      text: "CSS is single source of truth: 1) Reset+Tokens, 2) Navigation, 3) Buttons, 4) Sections, 5) Components, 6) Animations, 7) Responsive."
      priority: CRITICAL
    }
    rules {
      text: "Shared JS: IntersectionObserver scroll reveals, nav scroll state, smooth scroll, active nav link. No dependencies."
      priority: HIGH
    }
    rules {
      text: "Every page must have same nav with same links, same footer. Highlight active page."
      priority: HIGH
    }
    rules {
      text: "Expected sizes: HTML 8-12KB each, CSS 15-25KB, JS 1-2KB. Total excl images ~70-80KB for 5 pages."
      priority: NORMAL
    }
}
actions {
  id: "apply_anti_slop_design"
  description: "Apply anti-slop design principles for typography, color, layout, motion, content"
  trigger_phrases: "anti-slop"
  trigger_phrases: "design principles"
  trigger_phrases: "premium design"
  trigger_phrases: "editorial design"
    rules {
      text: "Typography: NEVER default to Inter/Poppins/Roboto only. Use editorial serif for display + modern sans for body + mono for specs."
      priority: CRITICAL
    }
    rules {
      text: "Color: NEVER purple-to-blue gradients as default. Choose distinctive muted accent. Near-black (#0a0a0a) not pure #000."
      priority: CRITICAL
    }
    rules {
      text: "Content: NEVER use buzzwords (revolutionize, cutting-edge, seamlessly). Do a DE-SLOP PASS before delivery."
      priority: CRITICAL
    }
    rules {
      text: "Layout: asymmetric grids (golden ratio 1:1.618, 60/40). Break grid intentionally. Vary section sizes dramatically."
      priority: HIGH
    }
    rules {
      text: "Motion: scroll reveals via IntersectionObserver. Staggered children. Custom easing curves. Every animation must justify existence."
      priority: HIGH
    }
    rules {
      text: "Texture: SVG grain overlay at 3% opacity. Vary border-radius across elements."
      priority: HIGH
    }
    rules {
      text: "Dramatic typography hierarchy: display at clamp(3rem,7vw,6.5rem) down to 0.7rem mono labels."
      priority: NORMAL
    }
    data {
      key: "anti_slop_patterns_to_grep"
      list_value {
        items {
          string_value: "not just"
        }
        items {
          string_value: "stops being"
        }
        items {
          string_value: "never a"
        }
        items {
          string_value: "—"
        }
        items {
          string_value: "revolutionize"
        }
        items {
          string_value: "cutting-edge"
        }
        items {
          string_value: "seamlessly"
        }
      }
    }
}
actions {
  id: "integrate_brand_assets"
  description: "Download and integrate brand assets from GitHub repos or URLs"
  trigger_phrases: "integrate brand assets"
  trigger_phrases: "add brand kit"
  trigger_phrases: "use brand images"
    rules {
      text: "Download via curl -sL from raw GitHub URLs. Check file sizes after download."
      priority: HIGH
    }
    rules {
      text: "Use logos in nav + footer, illustrations in product cards/about/features, brand mark in gallery."
      priority: HIGH
    }
    rules {
      text: "Raw URL pattern: https://raw.githubusercontent.com/{owner}/{repo}/main/{path}"
      priority: NORMAL
    }
}
actions {
  id: "build_page_templates"
  description: "Build individual page templates (home, products, developers, blog, about)"
  trigger_phrases: "build home page"
  trigger_phrases: "create products page"
  trigger_phrases: "about page template"
    rules {
      text: "Home: hero with editorial headline, metrics bar, thesis blockquote, product overview cards, image band, features, blog posts, CTA."
      priority: HIGH
    }
    rules {
      text: "Products: page header, product cards (alternating sides), full stack diagram, ecosystem grid, CTA."
      priority: HIGH
    }
    rules {
      text: "Developers: page header, code sample (terminal-style), SDK cards, network config panels, docs links, CTA."
      priority: HIGH
    }
    rules {
      text: "Don't make every page look the same — vary layouts: asymmetric grids, full-width bands, sticky sidebars."
      priority: NORMAL
    }
}
actions {
  id: "verify_site"
  description: "Verify site integrity before delivery"
  trigger_phrases: "verify site"
  trigger_phrases: "check the site"
  trigger_phrases: "site validation"
    rules {
      text: "Checklist: all HTML pages exist, CSS/JS load on every page, all images exist and non-zero, nav links correct, footer consistent, no broken links, responsive breakpoints defined."
      priority: CRITICAL
    }
    rules {
      text: "Test mobile at 375px width. Every layout must collapse gracefully."
      priority: HIGH
    }
    rules {
      text: "For zip delivery: python3 -c 'import shutil; shutil.make_archive(...)'"
      priority: NORMAL
    }
}

guardrails {
  text: "Run de-slop pass over all prose before delivery — grep for AI-slop patterns and rewrite"
  scope: ALWAYS
}

guardrails {
  text: "Nav and footer must be identical across all pages"
  scope: ALWAYS
}

guardrails {
  text: "Use --text-primary (#e8e6e3) for body text on dark backgrounds, not --text-secondary"
  scope: ALWAYS
}

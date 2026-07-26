meta {
  name: "research-publication-site"
  version: "1.0.0"
  summary: "Multi-page research publication site — auto-parse academic papers, category filters, paper pages"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "research site"
  keywords: "publication site"
  keywords: "academic site"
  keywords: "papers site"
  keywords: "research lab website"
  keywords: "research docs"
  intents: "build_research_site"
  intents: "host_papers"
  intents: "create_publication_site"
  patterns: "(build|create|make) .*(research|publication|academic) .*(site|website)"
  patterns: "(host|publish) .*(papers|publications|research)"
  patterns: "research lab .*(website|site)"
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
  capabilities: "research_sites"
  capabilities: "paper_auto_parsing"
  capabilities: "category_filters"
  capabilities: "academic_layouts"
  output_types: ".html"
  output_types: ".css"
  output_types: ".js"
}

actions {
  id: "build_research_architecture"
  description: "Build multi-page research site with paper hosting"
  trigger_phrases: "build research site"
  trigger_phrases: "set up publication site"
  trigger_phrases: "create paper hosting"
    rules {
      text: "Architecture: index.html (home), research.html (paper listing with filters), about.html, paper-<slug>.html (auto-generated per paper), css/style.css, js/main.js."
      priority: CRITICAL
    }
    rules {
      text: "This is a specialized variant of premium-website for text-heavy academic content. Compose with premium-website for shared CSS/JS patterns."
      priority: HIGH
    }
    rules {
      text: "Use premium-website's anti-slop principles but adapt for academic tone."
      priority: HIGH
    }
    rules {
      text: "Derive paper slugs from title, not filename. Slugs should be human-readable: paper-intent-compiler.html."
      priority: NORMAL
    }
}
actions {
  id: "auto_parse_papers"
  description: "Auto-parse academic .txt papers into structured HTML"
  trigger_phrases: "parse papers"
  trigger_phrases: "convert papers to html"
  trigger_phrases: "auto-generate paper pages"
    rules {
      text: "Regex patterns: Roman numeral sections (I. INTRODUCTION → <h3>), alphabetic subsections (A. Scaling Laws → <h4>), ABSTRACT label."
      priority: CRITICAL
    }
    rules {
      text: "Line-number stripping required — read_file() returns LINE_NUM|content format. Always strip before parsing."
      priority: CRITICAL
    }
    rules {
      text: "HTML-escape content before injecting into templates: replace &, <, > for math notation."
      priority: HIGH
    }
    rules {
      text: "Paper body text MUST use --text-primary (not --text-secondary) for readability on dark backgrounds."
      priority: HIGH
    }
    rules {
      text: "Do NOT use .reveal on paper content wrapper — IntersectionObserver may not trigger for below-fold content."
      priority: HIGH
    }
    rules {
      text: "Use execute_code to loop through papers and auto-generate. Manual writing of 14+ pages is error-prone."
      priority: NORMAL
    }
    data {
      key: "section_regex"
      string_value: "^([IVX]+\\.\\s+[A-Z][A-Z\\s]+)$"
    }
    data {
      key: "subsection_regex"
      string_value: "^([A-Z]\\.\\s+[A-Z].+)$"
    }
    data {
      key: "abstract_regex"
      string_value: "^(ABSTRACT|Abstract|INDEX TERMS|Index Terms)$"
    }
    examples {
      label: "paper parsing regex patterns"
      language: "python"
      code: "import re\n# Section headers\ncontent = re.sub(r'^([IVX]+\\.\\s+[A-Z][A-Z\\s]+)$', r'\\n<h3 class=\"paper-section\">\\1</h3>', content, flags=re.MULTILINE)\n# Subsection headers\ncontent = re.sub(r'^([A-Z]\\.\\s+[A-Z].+)$', r'\\n<h4 class=\"paper-subsection\">\\1</h4>', content, flags=re.MULTILINE)\n# Line-number stripping\nlines = [re.match(r'^\\d+\\|(.*)$', l).group(1) if re.match(r'^\\d+\\|(.*)$', l) else l for l in content.split('\\n')]"
    }
}
actions {
  id: "build_research_listing"
  description: "Build filterable paper listing page with category system"
  trigger_phrases: "paper listing"
  trigger_phrases: "category filters"
  trigger_phrases: "research listing page"
    rules {
      text: "Group papers into 4-7 research areas. Use data-cat attributes + JS filtering."
      priority: HIGH
    }
    rules {
      text: "Paper cards: grid layout with number, title, abstract excerpt, tags, arrow. Hover transitions."
      priority: HIGH
    }
    rules {
      text: "Filter buttons need smaller font/padding at 768px mobile breakpoint."
      priority: NORMAL
    }
    data {
      key: "optimal_categories"
      string_value: "4-7 categories (fewer makes filters pointless, more clutters UI)"
    }
}

guardrails {
  text: "Always strip line numbers from read_file() output before parsing papers"
  scope: ALWAYS
}

guardrails {
  text: "Paper body text must use --text-primary for dark background readability"
  scope: ALWAYS
}

guardrails {
  text: "HTML-escape all paper content before template injection"
  scope: ALWAYS
}

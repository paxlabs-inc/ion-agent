meta {
  name: "dogfood"
  version: "1.0.0"
  summary: "Exploratory QA of web apps: find bugs, collect evidence, generate reports"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "qa"
  keywords: "testing"
  keywords: "dogfood"
  keywords: "browser testing"
  keywords: "web testing"
  keywords: "bug report"
  intents: "qa_test"
  intents: "exploratory_test"
  intents: "bug_hunt"
  intents: "web_app_review"
  patterns: "(test|qa|dogfood|review) .*(web app|website|app|site)"
  patterns: "(find|hunt) .*(bug|issue|problem)"
  patterns: "exploratory testing"
}

requires {
  tools {
    name: "browser_navigate"
    required: true
  }
  tools {
    name: "browser_snapshot"
    required: true
  }
  tools {
    name: "browser_click"
    required: true
  }
  tools {
    name: "browser_type"
    required: true
  }
  tools {
    name: "browser_vision"
    required: true
  }
  tools {
    name: "browser_console"
    required: true
  }
  tools {
    name: "browser_scroll"
    required: true
  }
  tools {
    name: "browser_back"
    required: true
  }
  tools {
    name: "browser_press"
    required: true
  }
}

provides {
  capabilities: "web_qa"
  capabilities: "bug_detection"
  capabilities: "evidence_collection"
  capabilities: "report_generation"
  output_types: ".md"
  output_types: ".png"
}

actions {
  id: "plan"
  description: "Plan QA testing scope and create output structure"
  trigger_phrases: "start qa"
  trigger_phrases: "begin testing"
  trigger_phrases: "test this app"
    rules {
      text: "Create output dir structure: screenshots/ and report.md"
      priority: HIGH
    }
    rules {
      text: "Draft sitemap: landing page, nav links, key flows, forms, edge cases"
      priority: HIGH
    }
    rules {
      text: "Default output directory: ./dogfood-output"
      priority: NORMAL
    }
    data {
      key: "output_structure"
      map_value {
        entries {
          key: "screenshots"
          string_value: "{output_dir}/screenshots/"
        }
        entries {
          key: "report"
          string_value: "{output_dir}/report.md"
        }
      }
    }
    data {
      key: "coverage_areas"
      list_value {
        items {
          string_value: "Landing/home page"
        }
        items {
          string_value: "Navigation links (header, footer, sidebar)"
        }
        items {
          string_value: "Key user flows (sign up, login, search, checkout)"
        }
        items {
          string_value: "Forms and interactive elements"
        }
        items {
          string_value: "Edge cases (empty states, error pages, 404s)"
        }
      }
    }
}
actions {
  id: "explore"
  description: "Navigate pages and interact with elements to find bugs"
  trigger_phrases: "explore page"
  trigger_phrases: "test interactions"
  trigger_phrases: "check for bugs"
    rules {
      text: "Always check browser_console() after navigating and after significant interactions — silent JS errors are high-value findings"
      priority: CRITICAL
    }
    rules {
      text: "Use browser_vision with annotate=true to get element refs (@eN) for clicking"
      priority: HIGH
    }
    rules {
      text: "Test with both valid and invalid inputs — form validation bugs are common"
      priority: HIGH
    }
    rules {
      text: "Scroll through long pages — content below fold may have issues"
      priority: HIGH
    }
    rules {
      text: "Test navigation flows end-to-end through multi-step processes"
      priority: NORMAL
    }
    data {
      key: "workflow_per_page"
      list_value {
        items {
          string_value: "browser_navigate(url)"
        }
        items {
          string_value: "browser_snapshot()"
        }
        items {
          string_value: "browser_console(clear=true)"
        }
        items {
          string_value: "browser_vision(annotate=true)"
        }
        items {
          string_value: "Test interactive elements"
        }
        items {
          string_value: "browser_console() after interactions"
        }
      }
    }
}
actions {
  id: "collect_evidence"
  description: "Capture screenshots and document issues found"
  trigger_phrases: "document issue"
  trigger_phrases: "capture evidence"
  trigger_phrases: "screenshot bug"
    rules {
      text: "Take screenshot with browser_vision(annotate=false) for each issue"
      priority: HIGH
    }
    rules {
      text: "Record: URL, steps to reproduce, expected vs actual, console errors, screenshot path"
      priority: HIGH
    }
    rules {
      text: "Classify severity: Critical/High/Medium/Low and category: Functional/Visual/Accessibility/Console/UX/Content"
      priority: NORMAL
    }
}
actions {
  id: "categorize"
  description: "Review, de-duplicate, and prioritize collected issues"
  trigger_phrases: "prioritize bugs"
  trigger_phrases: "categorize issues"
  trigger_phrases: "review findings"
    rules {
      text: "De-duplicate — merge issues that are the same bug in different places"
      priority: HIGH
    }
    rules {
      text: "Sort by severity: Critical first, then High, Medium, Low"
      priority: HIGH
    }
    rules {
      text: "Count issues by severity and category for executive summary"
      priority: NORMAL
    }
}
actions {
  id: "report"
  description: "Generate final QA report with all findings"
  trigger_phrases: "generate report"
  trigger_phrases: "create bug report"
  trigger_phrases: "write qa report"
    rules {
      text: "Include: executive summary, per-issue sections with screenshots, summary table, testing notes"
      priority: HIGH
    }
    rules {
      text: "Use MEDIA:<screenshot_path> for inline screenshot references"
      priority: HIGH
    }
    rules {
      text: "Save report to {output_dir}/report.md"
      priority: NORMAL
    }
}

guardrails {
  text: "Always check browser_console() after navigating and after significant interactions"
  scope: ALWAYS
}

guardrails {
  text: "Never skip the verification pass — it catches inconsistencies easy to miss during writing"
  scope: ALWAYS
}

meta {
  name: "nano-pdf"
  version: "1.0.0"
  summary: "Edit PDF text/typos/titles via nano-pdf CLI with natural-language prompts"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "nano-pdf"
  keywords: "nano pdf"
  keywords: "pdf edit"
  keywords: "edit pdf"
  keywords: "fix pdf"
  keywords: "pdf typo"
  keywords: "change pdf text"
  intents: "pdf_edit"
  intents: "pdf_fix_typo"
  intents: "pdf_change_text"
  intents: "pdf_update_date"
  patterns: "(edit|fix|change|update|modify) .*(pdf|PDF)"
  patterns: "(correct|fix) .*(typo|text|title) .*(in|on) .*(pdf|PDF)"
  patterns: "nano.?pdf"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "python3"
}

provides {
  capabilities: "pdf_text_editing"
  capabilities: "pdf_nlp_editing"
  output_types: ".pdf"
}

actions {
  id: "install"
  description: "Install nano-pdf package"
  trigger_phrases: "install nano-pdf"
  trigger_phrases: "get nano-pdf"
    rules {
      text: "Install with uv (recommended): uv pip install nano-pdf"
      priority: HIGH
    }
    rules {
      text: "Or with pip: pip install nano-pdf"
      priority: NORMAL
    }
    rules {
      text: "Uses an LLM under the hood — requires an API key (check nano-pdf --help for config)"
      priority: NORMAL
    }
}
actions {
  id: "edit_pdf"
  description: "Edit PDF content using natural language instructions"
  trigger_phrases: "edit pdf"
  trigger_phrases: "fix pdf typo"
  trigger_phrases: "change pdf text"
  trigger_phrases: "update pdf title"
  trigger_phrases: "modify pdf"
    rules {
      text: "Page numbering may be 0-based or 1-based — if edit lands on wrong page, retry with ±1"
      priority: CRITICAL
    }
    rules {
      text: "Always verify the output PDF after editing (check file size or open it)"
      priority: HIGH
    }
    rules {
      text: "Works well for text changes; complex layout modifications need a different approach"
      priority: HIGH
    }
    rules {
      text: "Syntax: nano-pdf edit <file.pdf> <page_number> \"<instruction>\""
      priority: NORMAL
    }
    data {
      key: "command_pattern"
      string_value: "nano-pdf edit FILE.pdf PAGE_NUMBER \"NATURAL_LANGUAGE_INSTRUCTION\""
    }
    examples {
      label: "change a title"
      language: "bash"
      code: "nano-pdf edit deck.pdf 1 \"Change the title to 'Q3 Results' and fix the typo in the subtitle\""
    }
    examples {
      label: "update a date"
      language: "bash"
      code: "nano-pdf edit report.pdf 3 \"Update the date from January to February 2026\""
    }
    examples {
      label: "fix content"
      language: "bash"
      code: "nano-pdf edit contract.pdf 2 \"Change the client name from 'Acme Corp' to 'Acme Industries'\""
    }
}

guardrails {
  text: "Verify output PDF after editing — check file size or open to confirm changes applied"
  scope: ALWAYS
}

guardrails {
  text: "Text-only edits — complex layout changes need different tools (pymupdf, marker-pdf)"
  scope: ALWAYS
}

related {
  name: "ocr-and-documents"
  relationship: "composes_with"
  description: "For reading/extracting PDF content rather than editing"
}

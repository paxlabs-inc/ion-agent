meta {
  name: "ocr-and-documents"
  version: "2.3.0"
  summary: "Extract text from PDFs and scans using pymupdf or marker-pdf"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "pdf"
  keywords: "ocr"
  keywords: "extract text"
  keywords: "scanned document"
  keywords: "pymupdf"
  keywords: "marker-pdf"
  keywords: "arxiv"
  keywords: "text extraction"
  keywords: "document parsing"
  intents: "pdf_extract"
  intents: "pdf_ocr"
  intents: "pdf_to_markdown"
  intents: "pdf_split"
  intents: "pdf_merge"
  intents: "pdf_search"
  intents: "scan_extract"
  patterns: "(extract|read|parse|convert) .*(pdf|PDF|scan|document)"
  patterns: "(ocr|text extraction) .*(pdf|scan|image)"
  patterns: "arxiv .*(pdf|paper|paper)"
  patterns: "(split|merge|search) .*(pdf|PDF)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "python3"
}

provides {
  capabilities: "pdf_text_extraction"
  capabilities: "pdf_ocr"
  capabilities: "pdf_markdown"
  capabilities: "pdf_tables"
  capabilities: "pdf_images"
  capabilities: "pdf_split_merge"
  capabilities: "pdf_search"
  capabilities: "arxiv_extraction"
  output_types: ".md"
  output_types: ".json"
  output_types: ".txt"
  output_types: ".pdf"
}

actions {
  id: "extract_url"
  description: "Extract content from a PDF URL using web_extract"
  trigger_phrases: "extract pdf from url"
  trigger_phrases: "read pdf online"
  trigger_phrases: "download and extract pdf"
    rules {
      text: "ALWAYS try web_extract first when document has a URL — handles PDF-to-markdown via Firecrawl with no local deps"
      priority: CRITICAL
    }
    rules {
      text: "Only use local extraction when: file is local, web_extract fails, or batch processing needed"
      priority: HIGH
    }
    examples {
      label: "extract arxiv paper"
      language: "bash"
      code: "web_extract(urls=[\"https://arxiv.org/pdf/2402.03300\"])"
    }
}
actions {
  id: "extract_pymupdf"
  description: "Extract text/tables/images from PDF using pymupdf (lightweight)"
  trigger_phrases: "extract pdf text"
  trigger_phrases: "read pdf"
  trigger_phrases: "pdf to text"
  trigger_phrases: "pdf to markdown"
    rules {
      text: "Use pymupdf as safe default — instant, no models, ~25MB, works everywhere"
      priority: CRITICAL
    }
    rules {
      text: "pymupdf does NOT support OCR, equations, forms, or complex layout analysis"
      priority: HIGH
    }
    rules {
      text: "For OCR, scanned docs, equations, or complex layouts → use marker-pdf instead"
      priority: HIGH
    }
    data {
      key: "install_command"
      string_value: "pip install pymupdf pymupdf4llm"
    }
    data {
      key: "capabilities"
      map_value {
        entries {
          key: "text"
          bool_value: true
        }
        entries {
          key: "ocr"
          bool_value: false
        }
        entries {
          key: "tables"
          bool_value: true
        }
        entries {
          key: "equations"
          bool_value: false
        }
        entries {
          key: "images"
          bool_value: true
        }
        entries {
          key: "markdown"
          bool_value: true
        }
      }
    }
    examples {
      label: "extract as markdown"
      language: "bash"
      code: "python scripts/extract_pymupdf.py document.pdf --markdown"
    }
    examples {
      label: "extract specific pages"
      language: "bash"
      code: "python scripts/extract_pymupdf.py document.pdf --pages 0-4"
    }
    examples {
      label: "inline extraction"
      language: "bash"
      code: "python3 -c \"\nimport pymupdf\ndoc = pymupdf.open('document.pdf')\nfor page in doc:\n    print(page.get_text())\n\""
    }
}
actions {
  id: "extract_marker"
  description: "Extract text from PDF using marker-pdf (high-quality OCR)"
  trigger_phrases: "ocr pdf"
  trigger_phrases: "extract scanned pdf"
  trigger_phrases: "high quality pdf extraction"
    rules {
      text: "marker-pdf requires ~3-5GB (PyTorch + models) — check disk space first with scripts/extract_marker.py --check"
      priority: CRITICAL
    }
    rules {
      text: "Supports 90+ languages for OCR, tables, equations, code blocks, forms, header/footer removal"
      priority: HIGH
    }
    rules {
      text: "Speed: ~1-14s/page CPU, ~0.2s/page GPU"
      priority: HIGH
    }
    rules {
      text: "Models download ~2.5GB to ~/.cache/huggingface/ on first use"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "pip install marker-pdf"
    }
    data {
      key: "capabilities"
      map_value {
        entries {
          key: "text"
          bool_value: true
        }
        entries {
          key: "ocr"
          bool_value: true
        }
        entries {
          key: "tables"
          bool_value: true
        }
        entries {
          key: "equations"
          bool_value: true
        }
        entries {
          key: "images"
          bool_value: true
        }
        entries {
          key: "markdown"
          bool_value: true
        }
        entries {
          key: "languages"
          int_value: 90
        }
      }
    }
    examples {
      label: "extract as markdown"
      language: "bash"
      code: "python scripts/extract_marker.py document.pdf"
    }
    examples {
      label: "extract with JSON output"
      language: "bash"
      code: "python scripts/extract_marker.py document.pdf --json"
    }
    examples {
      label: "batch via CLI"
      language: "bash"
      code: "marker /path/to/folder --workers 4"
    }
}
actions {
  id: "split_merge"
  description: "Split, merge, or search within PDFs using pymupdf"
  trigger_phrases: "split pdf"
  trigger_phrases: "merge pdfs"
  trigger_phrases: "search pdf text"
  trigger_phrases: "combine pdfs"
    rules {
      text: "pymupdf handles split, merge, search natively — no extra dependencies"
      priority: HIGH
    }
    rules {
      text: "Use execute_code or inline Python for these operations"
      priority: NORMAL
    }
    examples {
      label: "split pages 1-5"
      language: "python"
      code: "import pymupdf\ndoc = pymupdf.open(\"report.pdf\")\nnew = pymupdf.open()\nfor i in range(5):\n    new.insert_pdf(doc, from_page=i, to_page=i)\nnew.save(\"pages_1-5.pdf\")"
    }
    examples {
      label: "merge multiple PDFs"
      language: "python"
      code: "import pymupdf\nresult = pymupdf.open()\nfor path in [\"a.pdf\", \"b.pdf\", \"c.pdf\"]:\n    result.insert_pdf(pymupdf.open(path))\nresult.save(\"merged.pdf\")"
    }
    examples {
      label: "search text across pages"
      language: "python"
      code: "import pymupdf\ndoc = pymupdf.open(\"report.pdf\")\nfor i, page in enumerate(doc):\n    results = page.search_for(\"revenue\")\n    if results:\n        print(f\"Page {i+1}: {len(results)} match(es)\")"
    }
}

guardrails {
  text: "Always try web_extract first for URLs — local extraction is fallback"
  scope: ALWAYS
}

guardrails {
  text: "pymupdf is safe default — use marker-pdf only when OCR/equations/forms/complex layouts needed"
  scope: ALWAYS
}

guardrails {
  text: "Check disk space before marker-pdf install — needs ~3-5GB"
  scope: WRITE_OPS
}

guardrails {
  text: "For Word docs use python-docx (parses structure) — better than OCR"
  scope: ALWAYS
}

guardrails {
  text: "For PowerPoint see powerpoint skill (uses python-pptx)"
  scope: ALWAYS
}

related {
  name: "powerpoint"
  relationship: "composes_with"
  description: "For PPTX file handling with python-pptx"
}

related {
  name: "nano-pdf"
  relationship: "composes_with"
  description: "For editing PDF text content via natural language"
}

meta {
  name: "powerpoint"
  version: "1.0.0"
  summary: "Create, read, edit .pptx decks, slides, notes, templates"
  author: "community"
  license: "Proprietary"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "powerpoint"
  keywords: "pptx"
  keywords: "slides"
  keywords: "deck"
  keywords: "presentation"
  keywords: "pitch deck"
  keywords: "slide deck"
  keywords: "slide show"
  intents: "create_presentation"
  intents: "read_presentation"
  intents: "edit_presentation"
  intents: "convert_presentation"
  patterns: "(create|make|build|design) .*(deck|slides|presentation|pptx)"
  patterns: "(read|parse|extract|analyze) .*(pptx|presentation|slides)"
  patterns: "(edit|modify|update) .*(pptx|presentation|slides|deck)"
  patterns: "(convert|export) .*(pptx|presentation) .*(pdf|images)"
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
  binaries: "node"
}

provides {
  capabilities: "pptx_create"
  capabilities: "pptx_read"
  capabilities: "pptx_edit"
  capabilities: "pptx_to_pdf"
  capabilities: "pptx_to_images"
  capabilities: "pptx_thumbnails"
  output_types: ".pptx"
  output_types: ".pdf"
  output_types: ".jpg"
}

actions {
  id: "read_content"
  description: "Read or extract text from a .pptx file"
  trigger_phrases: "read pptx"
  trigger_phrases: "extract presentation text"
  trigger_phrases: "parse slides"
  trigger_phrases: "analyze deck"
    rules {
      text: "Use python -m markitdown for text extraction"
      priority: HIGH
    }
    rules {
      text: "Use scripts/thumbnail.py for visual overview grid"
      priority: HIGH
    }
    rules {
      text: "Use scripts/office/unpack.py for raw XML inspection"
      priority: NORMAL
    }
    data {
      key: "extract_command"
      string_value: "python -m markitdown presentation.pptx"
    }
    data {
      key: "thumbnail_command"
      string_value: "python scripts/thumbnail.py presentation.pptx"
    }
    data {
      key: "unpack_command"
      string_value: "python scripts/office/unpack.py presentation.pptx unpacked/"
    }
    examples {
      label: "extract text content"
      language: "bash"
      code: "python -m markitdown presentation.pptx"
    }
}
actions {
  id: "edit_presentation"
  description: "Edit an existing .pptx file using a template"
  trigger_phrases: "edit pptx"
  trigger_phrases: "modify presentation"
  trigger_phrases: "update slides"
  trigger_phrases: "change deck"
    rules {
      text: "Read editing.md for full workflow: analyze template → unpack → manipulate → clean → pack"
      priority: CRITICAL
    }
    rules {
      text: "Analyze template with thumbnail.py before editing"
      priority: HIGH
    }
    rules {
      text: "Check for leftover placeholder text after editing: markitdown output | grep -iE 'xxxx|lorem|ipsum'"
      priority: HIGH
    }
    data {
      key: "workflow_steps"
      list_value {
        items {
          string_value: "1. Analyze template with thumbnail.py"
        }
        items {
          string_value: "2. Unpack → manipulate slides → edit content"
        }
        items {
          string_value: "3. Clean → pack"
        }
      }
    }
}
actions {
  id: "create_from_scratch"
  description: "Create a new .pptx presentation from scratch"
  trigger_phrases: "create presentation"
  trigger_phrases: "make slides"
  trigger_phrases: "build deck"
  trigger_phrases: "new pptx"
    rules {
      text: "Read pptxgenjs.md for full creation workflow — use pptxgenjs when no template available"
      priority: CRITICAL
    }
    rules {
      text: "NEVER use accent lines under titles — hallmark of AI-generated slides"
      priority: CRITICAL
    }
    rules {
      text: "Every slide needs a visual element — no text-only slides"
      priority: CRITICAL
    }
    rules {
      text: "Pick a bold content-informed color palette — never default to generic blue"
      priority: HIGH
    }
    rules {
      text: "Dark/light contrast: dark backgrounds for title+conclusion, light for content (sandwich)"
      priority: HIGH
    }
    rules {
      text: "Layout: vary across slides — two-column, icon+text rows, 2x2 grid, half-bleed image"
      priority: HIGH
    }
    rules {
      text: "Typography: header 36-44pt bold, body 14-16pt, min 0.5\" margins"
      priority: NORMAL
    }
    data {
      key: "color_palettes"
      map_value {
        entries {
          key: "midnight_executive"
          string_value: "1E2761/CADCFC/FFFFFF"
        }
        entries {
          key: "forest_moss"
          string_value: "2C5F2D/97BC62/F5F5F5"
        }
        entries {
          key: "coral_energy"
          string_value: "F96167/F9E795/2F3C7E"
        }
        entries {
          key: "warm_terracotta"
          string_value: "B85042/E7E8D1/A7BEAE"
        }
        entries {
          key: "ocean_gradient"
          string_value: "065A82/1C7293/21295C"
        }
        entries {
          key: "charcoal_minimal"
          string_value: "36454F/F2F2F2/212121"
        }
        entries {
          key: "teal_trust"
          string_value: "028090/00A896/02C39A"
        }
        entries {
          key: "berry_cream"
          string_value: "6D2E46/A26769/ECE2D0"
        }
        entries {
          key: "sage_calm"
          string_value: "84B59F/69A297/50808E"
        }
        entries {
          key: "cherry_bold"
          string_value: "990011/FCF6F5/2F3C7E"
        }
      }
    }
    data {
      key: "font_pairings"
      map_value {
        entries {
          key: "georgia"
          string_value: "Georgia / Calibri"
        }
        entries {
          key: "arial_black"
          string_value: "Arial Black / Arial"
        }
        entries {
          key: "trebuchet"
          string_value: "Trebuchet MS / Calibri"
        }
        entries {
          key: "palatino"
          string_value: "Palatino / Garamond"
        }
      }
    }
    data {
      key: "typography"
      map_value {
        entries {
          key: "slide_title"
          string_value: "36-44pt bold"
        }
        entries {
          key: "section_header"
          string_value: "20-24pt bold"
        }
        entries {
          key: "body"
          string_value: "14-16pt"
        }
        entries {
          key: "captions"
          string_value: "10-12pt muted"
        }
      }
    }
}
actions {
  id: "convert_to_images"
  description: "Convert presentation to images for visual QA"
  trigger_phrases: "convert pptx to images"
  trigger_phrases: "render slides"
  trigger_phrases: "slide images"
    rules {
      text: "Convert pptx → pdf via LibreOffice, then pdf → images via pdftoppm"
      priority: HIGH
    }
    rules {
      text: "Re-render specific slides: pdftoppm -f N -l N output.pdf slide-fixed"
      priority: NORMAL
    }
    data {
      key: "convert_command"
      string_value: "python scripts/office/soffice.py --headless --convert-to pdf output.pptx && pdftoppm -jpeg -r 150 output.pdf slide"
    }
    examples {
      label: "convert to slide images"
      language: "bash"
      code: "python scripts/office/soffice.py --headless --convert-to pdf output.pptx\npdftoppm -jpeg -r 150 output.pdf slide"
    }
}
actions {
  id: "qa_inspection"
  description: "Quality assurance — content and visual inspection"
  trigger_phrases: "check slides"
  trigger_phrases: "qa pptx"
  trigger_phrases: "inspect presentation"
  trigger_phrases: "verify slides"
    rules {
      text: "ALWAYS use subagents for visual QA — even for 2-3 slides. Fresh eyes find issues you'll miss"
      priority: CRITICAL
    }
    rules {
      text: "Assume there are problems — approach QA as a bug hunt, not confirmation"
      priority: CRITICAL
    }
    rules {
      text: "Do NOT declare success until at least one fix-and-verify cycle completed"
      priority: CRITICAL
    }
    rules {
      text: "Check for leftover placeholders: markitdown output | grep -iE 'xxxx|lorem|ipsum'"
      priority: HIGH
    }
    rules {
      text: "Visual inspection: overlapping elements, text overflow, low contrast, uneven gaps, alignment issues"
      priority: HIGH
    }
    rules {
      text: "Re-verify affected slides after fixes — one fix often creates another problem"
      priority: NORMAL
    }
    data {
      key: "content_qa_command"
      string_value: "python -m markitdown output.pptx"
    }
    data {
      key: "placeholder_check"
      string_value: "python -m markitdown output.pptx | grep -iE \"xxxx|lorem|ipsum|this.*(page|slide).*layout\""
    }
}

guardrails {
  text: "Never create boring slides — every slide needs visual elements, bold colors, varied layouts"
  scope: ALWAYS
}

guardrails {
  text: "NEVER use accent lines under titles — hallmark of AI-generated slides"
  scope: ALWAYS
}

guardrails {
  text: "Always run visual QA with subagents — don't trust your own first render"
  scope: ALWAYS
}

guardrails {
  text: "Check for leftover placeholder text before declaring success"
  scope: WRITE_OPS
}

guardrails {
  text: "Dependencies: markitdown[pptx], Pillow, pptxgenjs (npm), LibreOffice, Poppler"
  scope: ALWAYS
}

related {
  name: "ocr-and-documents"
  relationship: "composes_with"
  description: "For PDF text extraction when converting presentations"
}

meta {
  name: "humanizer"
  version: "2.5.1"
  summary: "Detect and eliminate AI writing patterns — 29 patterns for natural, human-sounding text"
  author: "Siqi Chen (@blader), Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "humanize"
  keywords: "de-AI"
  keywords: "de-slop"
  keywords: "anti-ai-slop"
  keywords: "rewrite"
  keywords: "natural voice"
  keywords: "AI tells"
  intents: "humanize_text"
  intents: "remove_ai_patterns"
  intents: "match_voice"
  intents: "review_ai_tells"
  patterns: "(humanize|de-AI|de-slop|un-ChatGPT) .*(text|writing|draft|prose)"
  patterns: "(rewrite|edit) .*(natural|human|voice)"
  patterns: "(doesn't|doesnt) sound (like|natural|human)"
  patterns: "AI .*(tell|pattern|slop|generated)"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "read_file"
    required: false
  }
}

provides {
  capabilities: "text_humanization"
  capabilities: "ai_pattern_detection"
  capabilities: "voice_calibration"
  capabilities: "anti_ai_editing"
  output_types: ".txt"
  output_types: ".md"
}

actions {
  id: "voice_calibration"
  description: "Analyze user's writing sample to match their voice"
  trigger_phrases: "match my voice"
  trigger_phrases: "use my style"
  trigger_phrases: "voice calibration"
    rules {
      text: "Read sample first: sentence length, word choice, paragraph starts, punctuation habits, transitions"
      priority: HIGH
    }
    rules {
      text: "Match their patterns — if they write short sentences, don't produce long ones"
      priority: HIGH
    }
    rules {
      text: "When no sample provided, fall back to natural, varied, opinionated voice"
      priority: NORMAL
    }
}
actions {
  id: "identify_patterns"
  description: "Scan text for 29 AI writing patterns across content, language, style, and communication"
  trigger_phrases: "find AI patterns"
  trigger_phrases: "scan for tells"
  trigger_phrases: "identify AI-isms"
    rules {
      text: "29 patterns across 4 categories: Content (1-6), Language (7-13), Style (14-19), Communication/Filler (20-29)"
      priority: CRITICAL
    }
    rules {
      text: "Content patterns: undue emphasis on significance, notability, -ing endings, promotional language, vague attributions, formulaic challenges sections"
      priority: HIGH
    }
    rules {
      text: "Language patterns: overused AI vocabulary, copula avoidance, negative parallelisms, rule of three, elegant variation, false ranges, passive voice"
      priority: HIGH
    }
    rules {
      text: "Style patterns: em dash overuse, boldface overuse, inline-header lists, title case, emojis, curly quotes"
      priority: HIGH
    }
    rules {
      text: "Communication/filler patterns: chatbot artifacts, knowledge-cutoff disclaimers, sycophantic tone, filler phrases, hedging, generic conclusions, hyphenated pairs, authority tropes, signposting, fragmented headers"
      priority: HIGH
    }
    data {
      key: "high_frequency_ai_words"
      list_value {
        items {
          string_value: "actually"
        }
        items {
          string_value: "additionally"
        }
        items {
          string_value: "crucial"
        }
        items {
          string_value: "delve"
        }
        items {
          string_value: "enhance"
        }
        items {
          string_value: "fostering"
        }
        items {
          string_value: "highlight"
        }
        items {
          string_value: "interplay"
        }
        items {
          string_value: "intricate"
        }
        items {
          string_value: "landscape"
        }
        items {
          string_value: "pivotal"
        }
        items {
          string_value: "showcase"
        }
        items {
          string_value: "tapestry"
        }
        items {
          string_value: "testament"
        }
        items {
          string_value: "underscore"
        }
        items {
          string_value: "vibrant"
        }
      }
    }
    data {
      key: "copula_avoidance_words"
      list_value {
        items {
          string_value: "serves as"
        }
        items {
          string_value: "stands as"
        }
        items {
          string_value: "marks"
        }
        items {
          string_value: "represents"
        }
        items {
          string_value: "boasts"
        }
        items {
          string_value: "features"
        }
        items {
          string_value: "offers"
        }
      }
    }
}
actions {
  id: "rewrite_humanize"
  description: "Rewrite problematic sections preserving meaning and adding soul"
  trigger_phrases: "rewrite text"
  trigger_phrases: "humanize this"
  trigger_phrases: "make it human"
    rules {
      text: "Preserve meaning — keep core message intact"
      priority: CRITICAL
    }
    rules {
      text: "Add soul: have opinions, vary rhythm, acknowledge complexity, use 'I' when it fits"
      priority: CRITICAL
    }
    rules {
      text: "Don't just remove bad patterns — inject actual personality"
      priority: HIGH
    }
    rules {
      text: "Final pass: ask 'What makes this so obviously AI generated?' then fix remaining tells"
      priority: HIGH
    }
    rules {
      text: "Show rewrite to user — for file edits, show diff or changed section"
      priority: NORMAL
    }
    data {
      key: "personality_tips"
      list_value {
        items {
          string_value: "Have opinions — react to facts, don't just report them"
        }
        items {
          string_value: "Vary rhythm — short punchy sentences, then longer flowing ones"
        }
        items {
          string_value: "Acknowledge complexity — real humans have mixed feelings"
        }
        items {
          string_value: "Use 'I' when it fits — first person is honest, not unprofessional"
        }
        items {
          string_value: "Let some mess in — perfect structure feels algorithmic"
        }
        items {
          string_value: "Be specific about feelings, not generic"
        }
      }
    }
}
actions {
  id: "process"
  description: "Full humanization workflow with audit"
  trigger_phrases: "full humanize"
  trigger_phrases: "humanize process"
  trigger_phrases: "edit and audit"
    rules {
      text: "Steps: read input → identify patterns → rewrite → verify natural sound → present draft → audit → final version"
      priority: HIGH
    }
    rules {
      text: "Output: draft rewrite, 'What makes this AI?' bullets, final rewrite, summary of changes"
      priority: HIGH
    }
    rules {
      text: "For file edits: use patch (targeted) or write_file (full rewrite) and show what changed"
      priority: NORMAL
    }
}

guardrails {
  text: "Apply this skill to your own output when writing user-facing prose"
  scope: ALWAYS
}

guardrails {
  text: "Show the rewrite to user — never silently overwrite"
  scope: WRITE_OPS
}

related {
  name: "songwriting-and-ai-music"
}

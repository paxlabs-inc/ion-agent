meta {
  name: "youtube-content"
  version: "1.0.0"
  summary: "Extract YouTube transcripts and transform into summaries, threads, blogs"
  author: "community"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "youtube"
  keywords: "transcript"
  keywords: "video summary"
  keywords: "youtube url"
  keywords: "youtu.be"
  keywords: "video transcript"
  keywords: "summarize video"
  intents: "youtube_transcript"
  intents: "summarize_video"
  intents: "extract_transcript"
  intents: "video_to_text"
  patterns: "(youtube|youtu\\.be).*(transcript|summary|summarize|extract)"
  patterns: "(transcript|summary|summarize) .*(youtube|video)"
  patterns: "https?://(www\\.)?(youtube\\.com|youtu\\.be)/"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "python3"
  binaries: "uv"
}

provides {
  capabilities: "youtube_transcript_extraction"
  capabilities: "video_summarization"
  capabilities: "content_transformation"
  output_types: ".md"
  output_types: ".txt"
  output_types: ".json"
}

actions {
  id: "setup"
  description: "Install youtube-transcript-api dependency"
  trigger_phrases: "set up youtube tool"
  trigger_phrases: "install youtube transcript"
    rules {
      text: "Install via: uv pip install youtube-transcript-api"
      priority: HIGH
    }
    data {
      key: "install_command"
      string_value: "uv pip install youtube-transcript-api"
    }
}
actions {
  id: "fetch_transcript"
  description: "Extract transcript from a YouTube video"
  trigger_phrases: "get youtube transcript"
  trigger_phrases: "extract transcript"
  trigger_phrases: "fetch video transcript"
  trigger_phrases: "transcribe this video"
    rules {
      text: "Accepts standard YouTube URLs, shorts, embeds, live links, or raw 11-char video IDs"
      priority: CRITICAL
    }
    rules {
      text: "Use --text-only --timestamps for structured processing"
      priority: HIGH
    }
    rules {
      text: "If transcript is empty, retry without --language to get any available transcript"
      priority: HIGH
    }
    rules {
      text: "If still empty, tell user the video likely has transcripts disabled"
      priority: HIGH
    }
    rules {
      text: "For transcripts >50K chars, split into ~40K chunks with 2K overlap before summarizing"
      priority: NORMAL
    }
    data {
      key: "script_path"
      string_value: "SKILL_DIR/scripts/fetch_transcript.py"
    }
    data {
      key: "output_formats"
      list_value {
        items {
          string_value: "JSON with metadata (default)"
        }
        items {
          string_value: "Plain text (--text-only)"
        }
        items {
          string_value: "With timestamps (--timestamps)"
        }
        items {
          string_value: "Specific language (--language tr,en)"
        }
      }
    }
    examples {
      label: "JSON output with metadata"
      language: "bash"
      code: "uv run python3 SKILL_DIR/scripts/fetch_transcript.py \"https://youtube.com/watch?v=VIDEO_ID\""
    }
    examples {
      label: "plain text output"
      language: "bash"
      code: "uv run python3 SKILL_DIR/scripts/fetch_transcript.py \"URL\" --text-only"
    }
    examples {
      label: "with timestamps"
      language: "bash"
      code: "uv run python3 SKILL_DIR/scripts/fetch_transcript.py \"URL\" --timestamps"
    }
    examples {
      label: "specific language with fallback"
      language: "bash"
      code: "uv run python3 SKILL_DIR/scripts/fetch_transcript.py \"URL\" --language tr,en"
    }
}
actions {
  id: "transform_content"
  description: "Transform transcript into structured formats (chapters, summary, thread, blog)"
  trigger_phrases: "summarize this video"
  trigger_phrases: "create chapters"
  trigger_phrases: "make a thread from this video"
  trigger_phrases: "blog post from video"
    rules {
      text: "Fetch transcript first, then transform. Default to summary if user doesn't specify format"
      priority: CRITICAL
    }
    rules {
      text: "Chapters: group by topic shifts with timestamped chapter list"
      priority: HIGH
    }
    rules {
      text: "Thread: numbered posts each under 280 chars"
      priority: HIGH
    }
    rules {
      text: "Verify output for coherence, correct timestamps, and completeness before presenting"
      priority: HIGH
    }
    rules {
      text: "Supported formats: chapters, summary, chapter_summaries, thread, blog_post, quotes"
      priority: NORMAL
    }
    data {
      key: "output_formats"
      map_value {
        entries {
          key: "chapters"
          string_value: "Group by topic shifts, timestamped chapter list"
        }
        entries {
          key: "summary"
          string_value: "5-10 sentence overview"
        }
        entries {
          key: "chapter_summaries"
          string_value: "Chapters with short paragraph each"
        }
        entries {
          key: "thread"
          string_value: "Twitter/X thread, numbered, <280 chars each"
        }
        entries {
          key: "blog_post"
          string_value: "Full article with title, sections, takeaways"
        }
        entries {
          key: "quotes"
          string_value: "Notable quotes with timestamps"
        }
      }
    }
    examples {
      label: "chapters output format"
      language: "text"
      code: "00:00 Introduction — host opens with the problem statement\n03:45 Background — prior work and why existing solutions fall short\n12:20 Core method — walkthrough of the proposed approach\n24:10 Results — benchmark comparisons and key takeaways\n31:55 Q&A — audience questions on scalability and next steps"
    }
}

guardrails {
  text: "Always validate transcript is non-empty before transforming"
  scope: ALWAYS
}

guardrails {
  text: "Retry without --language flag if no transcript found for requested language"
  scope: ALWAYS
}

guardrails {
  text: "If dependency missing, run uv pip install youtube-transcript-api and retry"
  scope: ALWAYS
}

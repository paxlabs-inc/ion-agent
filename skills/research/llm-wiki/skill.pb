meta {
  name: "llm-wiki"
  version: "2.1.0"
  summary: "Karpathy's LLM Wiki: build and query interlinked markdown knowledge base"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "wiki"
  keywords: "knowledge base"
  keywords: "llm wiki"
  keywords: "karpathy wiki"
  keywords: "interlinked notes"
  keywords: "rag alternative"
  intents: "create_wiki"
  intents: "ingest_source"
  intents: "query_wiki"
  intents: "lint_wiki"
  intents: "build_knowledge_base"
  patterns: "(create|build|start) .*(wiki|knowledge base)"
  patterns: "(ingest|add|process) .*(source|article|paper).*wiki"
  patterns: "(lint|audit|health.?check) .*(wiki|knowledge base)"
  patterns: "wiki .*(query|search|question)"
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
  tools {
    name: "read_file"
    required: true
  }
  tools {
    name: "web_extract"
    required: false
  }
}

provides {
  capabilities: "knowledge_base_management"
  capabilities: "source_ingestion"
  capabilities: "wiki_querying"
  capabilities: "wiki_linting"
  capabilities: "cross_referencing"
  output_types: ".md"
}

actions {
  id: "initialize_wiki"
  description: "Create a new wiki with directory structure, schema, and index"
  trigger_phrases: "create a wiki"
  trigger_phrases: "start a wiki"
  trigger_phrases: "initialize wiki"
  trigger_phrases: "new knowledge base"
    rules {
      text: "Wiki location from WIKI_PATH env var, default ~/wiki"
      priority: CRITICAL
    }
    rules {
      text: "Create three layers: raw/ (immutable sources), entities/ + concepts/ + comparisons/ + queries/ (agent-owned), SCHEMA.md (conventions)"
      priority: CRITICAL
    }
    rules {
      text: "Ask user what domain the wiki covers before writing SCHEMA.md"
      priority: HIGH
    }
    rules {
      text: "SCHEMA.md must define: domain, file naming conventions, frontmatter format, tag taxonomy, page thresholds, update policy"
      priority: HIGH
    }
    rules {
      text: "Initial files: SCHEMA.md, index.md (sectioned catalog), log.md (creation entry)"
      priority: HIGH
    }
    rules {
      text: "Suggest first sources to ingest after wiki is ready"
      priority: NORMAL
    }
    data {
      key: "directory_structure"
      map_value {
        entries {
          key: "schema"
          string_value: "SCHEMA.md"
        }
        entries {
          key: "index"
          string_value: "index.md"
        }
        entries {
          key: "log"
          string_value: "log.md"
        }
        entries {
          key: "raw_articles"
          string_value: "raw/articles/"
        }
        entries {
          key: "raw_papers"
          string_value: "raw/papers/"
        }
        entries {
          key: "raw_transcripts"
          string_value: "raw/transcripts/"
        }
        entries {
          key: "raw_assets"
          string_value: "raw/assets/"
        }
        entries {
          key: "entities"
          string_value: "entities/"
        }
        entries {
          key: "concepts"
          string_value: "concepts/"
        }
        entries {
          key: "comparisons"
          string_value: "comparisons/"
        }
        entries {
          key: "queries"
          string_value: "queries/"
        }
      }
    }
}
actions {
  id: "orient_to_wiki"
  description: "Read schema, index, and recent log before any wiki operation"
  trigger_phrases: "resume wiki"
  trigger_phrases: "open wiki"
  trigger_phrases: "continue working on wiki"
    rules {
      text: "ALWAYS read SCHEMA.md, index.md, and recent log.md (last 20-30 entries) before any operation in a new session"
      priority: CRITICAL
    }
    rules {
      text: "Skipping orientation causes duplicates, missed cross-references, and schema violations"
      priority: CRITICAL
    }
    rules {
      text: "For large wikis (100+ pages): also search_files for the topic before creating anything new"
      priority: HIGH
    }
    data {
      key: "orientation_reads"
      list_value {
        items {
          string_value: "SCHEMA.md — domain, conventions, tag taxonomy"
        }
        items {
          string_value: "index.md — existing pages and summaries"
        }
        items {
          string_value: "log.md — recent activity (last 20-30 entries)"
        }
      }
    }
}
actions {
  id: "ingest_source"
  description: "Integrate a source (URL, file, paste) into the wiki"
  trigger_phrases: "ingest source"
  trigger_phrases: "add to wiki"
  trigger_phrases: "process article"
  trigger_phrases: "add source"
    rules {
      text: "Step 1: Capture raw source (URL→web_extract→raw/articles/, PDF→raw/papers/) with source_url, ingested, sha256 frontmatter"
      priority: CRITICAL
    }
    rules {
      text: "Step 2: Check existing pages before creating new ones — search index.md and search_files"
      priority: CRITICAL
    }
    rules {
      text: "Step 3: Create/update wiki pages only if entities meet Page Thresholds (2+ source mentions or central to one source)"
      priority: CRITICAL
    }
    rules {
      text: "Every new/updated page must link to at least 2 other pages via [[wikilinks]]"
      priority: HIGH
    }
    rules {
      text: "Update index.md, log.md after every ingest"
      priority: HIGH
    }
    rules {
      text: "On re-ingest of same URL: recompute sha256, skip if identical, flag drift if different"
      priority: HIGH
    }
    rules {
      text: "Provenance markers: on pages synthesizing 3+ sources, append ^[raw/articles/source.md] to sourced paragraphs"
      priority: NORMAL
    }
    rules {
      text: "Set confidence field in frontmatter for opinion-heavy or single-source claims"
      priority: NORMAL
    }
    data {
      key: "page_types"
      list_value {
        items {
          string_value: "entity"
        }
        items {
          string_value: "concept"
        }
        items {
          string_value: "comparison"
        }
        items {
          string_value: "query"
        }
        items {
          string_value: "summary"
        }
      }
    }
    data {
      key: "frontmatter_fields"
      list_value {
        items {
          string_value: "title"
        }
        items {
          string_value: "created"
        }
        items {
          string_value: "updated"
        }
        items {
          string_value: "type"
        }
        items {
          string_value: "tags"
        }
        items {
          string_value: "sources"
        }
        items {
          string_value: "confidence"
        }
        items {
          string_value: "contested"
        }
        items {
          string_value: "contradictions"
        }
      }
    }
}
actions {
  id: "query_wiki"
  description: "Answer questions using wiki knowledge base"
  trigger_phrases: "ask the wiki"
  trigger_phrases: "wiki search"
  trigger_phrases: "what does the wiki say about"
    rules {
      text: "Read index.md to identify relevant pages, then read those pages"
      priority: HIGH
    }
    rules {
      text: "For wikis with 100+ pages: also search_files across all .md files for key terms"
      priority: HIGH
    }
    rules {
      text: "Synthesize answer from compiled knowledge, cite wiki pages: 'Based on [[page-a]] and [[page-b]]...'"
      priority: HIGH
    }
    rules {
      text: "File valuable answers back — create queries/ or comparisons/ page for substantial syntheses"
      priority: NORMAL
    }
    rules {
      text: "Update log.md with query and whether it was filed"
      priority: NORMAL
    }
}
actions {
  id: "lint_wiki"
  description: "Audit wiki for orphans, broken links, staleness, contradictions"
  trigger_phrases: "lint wiki"
  trigger_phrases: "health check wiki"
  trigger_phrases: "audit wiki"
    rules {
      text: "Check: orphan pages (no inbound wikilinks), broken wikilinks, index completeness, frontmatter validation"
      priority: HIGH
    }
    rules {
      text: "Check: stale content (>90 days since last update), contradictions (pages with contested:true), confidence:low pages"
      priority: HIGH
    }
    rules {
      text: "Check: source drift (recompute sha256 for raw/ files), page size (>200 lines), tag audit (all tags in taxonomy)"
      priority: HIGH
    }
    rules {
      text: "Report grouped by severity: broken links > orphans > source drift > contested pages > stale content > style"
      priority: NORMAL
    }
    rules {
      text: "Rotate log.md if >500 entries: rename to log-YYYY.md, start fresh"
      priority: NORMAL
    }
}

guardrails {
  text: "Never modify files in raw/ — sources are immutable. Corrections go in wiki pages"
  scope: ALWAYS
}

guardrails {
  text: "Always orient first: read SCHEMA + index + recent log before any wiki operation"
  scope: ALWAYS
}

guardrails {
  text: "Always update index.md and log.md after every wiki operation"
  scope: ALWAYS
}

guardrails {
  text: "Every wiki page must have frontmatter and at least 2 outbound [[wikilinks]]"
  scope: ALWAYS
}

guardrails {
  text: "Tags must come from SCHEMA.md taxonomy — add new tags there first, then use them"
  scope: ALWAYS
}

related {
  name: "arxiv"
  relationship: "composes_with"
  description: "Search arXiv for papers to ingest into wiki"
}

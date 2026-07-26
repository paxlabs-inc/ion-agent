meta {
  name: "blogwatcher"
  version: "2.0.0"
  summary: "Monitor blogs and RSS/Atom feeds via blogwatcher-cli tool"
  author: "JulienTant (fork of Hyaxia/blogwatcher)"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "blogwatcher"
  keywords: "rss"
  keywords: "atom feed"
  keywords: "blog monitor"
  keywords: "feed reader"
  keywords: "opml"
  intents: "monitor_blogs"
  intents: "scan_feeds"
  intents: "manage_subscriptions"
  intents: "read_articles"
  patterns: "(monitor|track|watch) .*(blog|feed|rss)"
  patterns: "blogwatcher"
  patterns: "(add|import|scan) .*(feed|blog|rss)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  binaries: "blogwatcher-cli"
}

provides {
  capabilities: "blog_monitoring"
  capabilities: "rss_feed_reading"
  capabilities: "article_tracking"
  capabilities: "opml_import"
}

actions {
  id: "install"
  description: "Install blogwatcher-cli binary"
  trigger_phrases: "install blogwatcher"
  trigger_phrases: "set up blogwatcher"
    rules {
      text: "Go install: go install github.com/JulienTant/blogwatcher-cli/cmd/blogwatcher-cli@latest"
      priority: HIGH
    }
    rules {
      text: "Binary download: curl the platform-specific tarball from GitHub releases to /usr/local/bin"
      priority: HIGH
    }
    rules {
      text: "Docker: docker run --rm -v blogwatcher-cli:/data ghcr.io/julientant/blogwatcher-cli"
      priority: HIGH
    }
    rules {
      text: "Docker persistent storage: use BLOGWATCHER_DB env or named volume for database"
      priority: NORMAL
    }
    data {
      key: "install_commands"
      map_value {
        entries {
          key: "go"
          string_value: "go install github.com/JulienTant/blogwatcher-cli/cmd/blogwatcher-cli@latest"
        }
        entries {
          key: "docker"
          string_value: "docker run --rm -v blogwatcher-cli:/data ghcr.io/julientant/blogwatcher-cli"
        }
      }
    }
    data {
      key: "releases_url"
      string_value: "https://github.com/JulienTant/blogwatcher-cli/releases"
    }
    data {
      key: "default_db_path"
      string_value: "~/.blogwatcher-cli/blogwatcher-cli.db"
    }
}
actions {
  id: "manage_blogs"
  description: "Add, remove, list, and import blogs"
  trigger_phrases: "add blog"
  trigger_phrases: "remove blog"
  trigger_phrases: "list blogs"
  trigger_phrases: "import opml"
    rules {
      text: "Add blog: blogwatcher-cli add 'My Blog' https://example.com"
      priority: HIGH
    }
    rules {
      text: "Add with explicit feed: blogwatcher-cli add 'My Blog' URL --feed-url FEED_URL"
      priority: HIGH
    }
    rules {
      text: "Add with HTML scraping fallback: blogwatcher-cli add 'My Blog' URL --scrape-selector 'article h2 a'"
      priority: HIGH
    }
    rules {
      text: "List tracked: blogwatcher-cli blogs | Remove: blogwatcher-cli remove 'Name' --yes"
      priority: NORMAL
    }
    rules {
      text: "Import OPML: blogwatcher-cli import subscriptions.opml"
      priority: NORMAL
    }
    data {
      key: "env_vars"
      map_value {
        entries {
          key: "BLOGWATCHER_DB"
          string_value: "Path to SQLite database file"
        }
        entries {
          key: "BLOGWATCHER_WORKERS"
          string_value: "Concurrent scan workers (default: 8)"
        }
        entries {
          key: "BLOGWATCHER_SILENT"
          string_value: "Only output 'scan done' when scanning"
        }
        entries {
          key: "BLOGWATCHER_YES"
          string_value: "Skip confirmation prompts"
        }
      }
    }
}
actions {
  id: "scan_and_read"
  description: "Scan feeds for new articles and manage read/unread state"
  trigger_phrases: "scan blogs"
  trigger_phrases: "check feeds"
  trigger_phrases: "new articles"
  trigger_phrases: "unread articles"
    rules {
      text: "Scan all: blogwatcher-cli scan | Scan one: blogwatcher-cli scan 'Blog Name'"
      priority: HIGH
    }
    rules {
      text: "List unread: blogwatcher-cli articles | List all: blogwatcher-cli articles --all"
      priority: HIGH
    }
    rules {
      text: "Filter by blog: --blog 'Name' | Filter by category: --category 'Category'"
      priority: HIGH
    }
    rules {
      text: "Mark read: blogwatcher-cli read ID | Mark unread: blogwatcher-cli unread ID"
      priority: NORMAL
    }
    rules {
      text: "Mark all read: blogwatcher-cli read-all [--blog 'Name' --yes]"
      priority: NORMAL
    }
    examples {
      label: "scan and list new articles"
      language: "bash"
      code: "blogwatcher-cli scan\nblogwatcher-cli articles\nblogwatcher-cli articles --blog \"Engineering\" --category \"Tech\""
    }
}

guardrails {
  text: "Database defaults to ~/.blogwatcher-cli/blogwatcher-cli.db — override with BLOGWATCHER_DB"
  scope: ALWAYS
}

guardrails {
  text: "From original blogwatcher migration: mv ~/.blogwatcher/blogwatcher.db ~/.blogwatcher-cli/blogwatcher-cli.db"
  scope: ALWAYS
}

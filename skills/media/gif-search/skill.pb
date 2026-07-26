meta {
  name: "gif-search"
  version: "1.1.0"
  summary: "Search/download GIFs from Tenor via curl + jq"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "gif"
  keywords: "tenor"
  keywords: "reaction gif"
  keywords: "animated gif"
  keywords: "search gif"
  keywords: "download gif"
  intents: "search_gifs"
  intents: "download_gif"
  intents: "find_gif"
  intents: "gif_search"
  patterns: "(search|find|get|download) .*(gif|gifs|reaction)"
  patterns: "tenor"
  patterns: "gif (for|of|about) .+"
}

requires {
  env_all: "TENOR_API_KEY"
  tools {
    name: "terminal"
    required: true
  }
  binaries: "curl"
  binaries: "jq"
}

provides {
  capabilities: "gif_search"
  capabilities: "gif_download"
  capabilities: "gif_metadata"
  output_types: ".gif"
}

actions {
  id: "search_gifs"
  description: "Search Tenor for GIFs matching a query"
  trigger_phrases: "search for gifs"
  trigger_phrases: "find a gif"
  trigger_phrases: "look up gif"
  trigger_phrases: "reaction gif"
    rules {
      text: "URL-encode the query — use + for spaces and %XX for special characters"
      priority: CRITICAL
    }
    rules {
      text: "Use tinygif URLs for chat/bandwidth-constrained contexts"
      priority: HIGH
    }
    rules {
      text: "Default limit is 20; max is 50"
      priority: HIGH
    }
    rules {
      text: "GIF URLs work directly in markdown: ![alt](url)"
      priority: NORMAL
    }
    data {
      key: "base_url"
      string_value: "https://tenor.googleapis.com/v2/search"
    }
    data {
      key: "api_parameters"
      map_value {
        entries {
          key: "q"
          string_value: "Search query (URL-encode spaces as +)"
        }
        entries {
          key: "limit"
          string_value: "Max results 1-50, default 20"
        }
        entries {
          key: "key"
          string_value: "API key from $TENOR_API_KEY"
        }
        entries {
          key: "media_filter"
          string_value: "Filter: gif, tinygif, mp4, tinymp4, webm"
        }
        entries {
          key: "contentfilter"
          string_value: "Safety: off, low, medium, high"
        }
        entries {
          key: "locale"
          string_value: "Language: en_US, es, fr, etc."
        }
      }
    }
    data {
      key: "media_formats"
      map_value {
        entries {
          key: "gif"
          string_value: "Full quality GIF"
        }
        entries {
          key: "tinygif"
          string_value: "Small preview GIF"
        }
        entries {
          key: "mp4"
          string_value: "Video version (smaller file size)"
        }
        entries {
          key: "tinymp4"
          string_value: "Small preview video"
        }
        entries {
          key: "webm"
          string_value: "WebM video"
        }
        entries {
          key: "nanogif"
          string_value: "Tiny thumbnail"
        }
      }
    }
    examples {
      label: "search and get GIF URLs"
      language: "bash"
      code: "curl -s \"https://tenor.googleapis.com/v2/search?q=thumbs+up&limit=5&key=${TENOR_API_KEY}\" | jq -r '.results[].media_formats.gif.url'"
    }
    examples {
      label: "get smaller preview versions"
      language: "bash"
      code: "curl -s \"https://tenor.googleapis.com/v2/search?q=nice+work&limit=3&key=${TENOR_API_KEY}\" | jq -r '.results[].media_formats.tinygif.url'"
    }
}
actions {
  id: "download_gif"
  description: "Search and download a GIF to a local file"
  trigger_phrases: "download a gif"
  trigger_phrases: "save gif"
  trigger_phrases: "download the top gif"
    rules {
      text: "Search first to get URL, then curl -sL to download"
      priority: HIGH
    }
    rules {
      text: "Save as .gif extension"
      priority: NORMAL
    }
    examples {
      label: "search and download top result"
      language: "bash"
      code: "URL=$(curl -s \"https://tenor.googleapis.com/v2/search?q=celebration&limit=1&key=${TENOR_API_KEY}\" | jq -r '.results[0].media_formats.gif.url')\ncurl -sL \"$URL\" -o celebration.gif"
    }
}
actions {
  id: "get_metadata"
  description: "Get full metadata for GIF search results"
  trigger_phrases: "gif details"
  trigger_phrases: "gif metadata"
  trigger_phrases: "gif info"
    rules {
      text: "Include title, URL, preview URL, and dimensions"
      priority: HIGH
    }
    examples {
      label: "get full metadata"
      language: "bash"
      code: "curl -s \"https://tenor.googleapis.com/v2/search?q=cat&limit=3&key=${TENOR_API_KEY}\" | jq '.results[] | {title: .title, url: .media_formats.gif.url, preview: .media_formats.tinygif.url, dimensions: .media_formats.gif.dims}'"
    }
}

guardrails {
  text: "URL-encode search queries — use + for spaces"
  scope: ALWAYS
}

guardrails {
  text: "Never expose TENOR_API_KEY in output"
  scope: ALWAYS
}

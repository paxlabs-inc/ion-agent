meta {
  name: "arxiv"
  version: "1.0.0"
  summary: "Search arXiv papers by keyword, author, category, or ID; Semantic Scholar citations"
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "arxiv"
  keywords: "arXiv"
  keywords: "paper search"
  keywords: "academic paper"
  keywords: "semantic scholar"
  keywords: "bibtex"
  keywords: "citation"
  intents: "search_papers"
  intents: "get_paper"
  intents: "read_abstract"
  intents: "generate_bibtex"
  intents: "find_citations"
  intents: "find_references"
  patterns: "(search|find|look up) .*(paper|arxiv|academic)"
  patterns: "arxiv\\.org/(abs|pdf)/"
  patterns: "(generate|create) .*(bibtex|citation)"
  patterns: "(who cited|citations of|references from)"
}

requires {
  tools {
    name: "terminal"
    required: true
  }
  tools {
    name: "web_extract"
    required: false
  }
  binaries: "curl"
  binaries: "python3"
}

provides {
  capabilities: "arxiv_search"
  capabilities: "arxiv_fetch"
  capabilities: "bibtex_generation"
  capabilities: "semantic_scholar_citations"
  capabilities: "paper_reading"
  output_types: ".bib"
}

actions {
  id: "search_papers"
  description: "Search arXiv for papers by keyword, author, or category"
  trigger_phrases: "search arxiv"
  trigger_phrases: "find papers about"
  trigger_phrases: "search for papers"
  trigger_phrases: "arxiv search"
    rules {
      text: "arXiv API returns Atom XML — use helper script or python3 parsing for clean output"
      priority: CRITICAL
    }
    rules {
      text: "Rate limit: ~1 request per 3 seconds for arXiv API"
      priority: CRITICAL
    }
    rules {
      text: "Search query prefixes: all: (all fields), ti: (title), au: (author), abs: (abstract), cat: (category)"
      priority: HIGH
    }
    rules {
      text: "Boolean operators: + for AND, OR, ANDNOT. Exact phrases in double quotes"
      priority: HIGH
    }
    rules {
      text: "Sort options: sortBy=relevance|lastUpdatedDate|submittedDate, sortOrder=ascending|descending"
      priority: NORMAL
    }
    rules {
      text: "max_results default 10, max 30000. Use start for pagination"
      priority: NORMAL
    }
    data {
      key: "api_base_url"
      string_value: "https://export.arxiv.org/api/query"
    }
    data {
      key: "common_categories"
      map_value {
        entries {
          key: "cs.AI"
          string_value: "Artificial Intelligence"
        }
        entries {
          key: "cs.CL"
          string_value: "Computation and Language (NLP)"
        }
        entries {
          key: "cs.CV"
          string_value: "Computer Vision"
        }
        entries {
          key: "cs.LG"
          string_value: "Machine Learning"
        }
        entries {
          key: "cs.CR"
          string_value: "Cryptography and Security"
        }
        entries {
          key: "stat.ML"
          string_value: "Machine Learning (Statistics)"
        }
      }
    }
    examples {
      label: "search by keyword with clean output"
      language: "bash"
      code: "curl -s \"https://export.arxiv.org/api/query?search_query=all:GRPO+reinforcement+learning&max_results=5&sortBy=submittedDate&sortOrder=descending\" | python3 -c \"\nimport sys, xml.etree.ElementTree as ET\nns = {'a': 'http://www.w3.org/2005/Atom'}\nroot = ET.parse(sys.stdin).getroot()\nfor i, entry in enumerate(root.findall('a:entry', ns)):\n    title = entry.find('a:title', ns).text.strip().replace('\\n', ' ')\n    arxiv_id = entry.find('a:id', ns).text.strip().split('/abs/')[-1]\n    published = entry.find('a:published', ns).text[:10]\n    print(f'{i+1}. [{arxiv_id}] {title} ({published})')\n\""
    }
}
actions {
  id: "fetch_paper"
  description: "Fetch a specific paper by arXiv ID"
  trigger_phrases: "get paper"
  trigger_phrases: "fetch paper"
  trigger_phrases: "arxiv paper"
  trigger_phrases: "read paper"
    rules {
      text: "By arXiv ID: curl 'https://export.arxiv.org/api/query?id_list=2402.03300'"
      priority: HIGH
    }
    rules {
      text: "Multiple papers: comma-separated IDs in id_list parameter"
      priority: HIGH
    }
    rules {
      text: "Read abstract page: web_extract(urls=['https://arxiv.org/abs/ID'])"
      priority: HIGH
    }
    rules {
      text: "Read full paper: web_extract(urls=['https://arxiv.org/pdf/ID'])"
      priority: HIGH
    }
    rules {
      text: "arXiv IDs: old format (hep-th/0601001) vs new (2402.03300)"
      priority: NORMAL
    }
    rules {
      text: "Versioned URLs: abs/1706.03762 resolves to latest, abs/1706.03762v1 to specific version"
      priority: NORMAL
    }
    rules {
      text: "Check for withdrawn papers: summary field contains withdrawal notice"
      priority: NORMAL
    }
    data {
      key: "url_patterns"
      map_value {
        entries {
          key: "abstract"
          string_value: "https://arxiv.org/abs/{id}"
        }
        entries {
          key: "pdf"
          string_value: "https://arxiv.org/pdf/{id}"
        }
        entries {
          key: "html"
          string_value: "https://arxiv.org/html/{id}"
        }
        entries {
          key: "api"
          string_value: "https://export.arxiv.org/api/query?id_list={id}"
        }
      }
    }
}
actions {
  id: "generate_bibtex"
  description: "Generate BibTeX entry from arXiv metadata"
  trigger_phrases: "generate bibtex"
  trigger_phrases: "create citation"
  trigger_phrases: "bibtex for paper"
    rules {
      text: "NEVER generate BibTeX from memory — always fetch metadata programmatically from arXiv API"
      priority: CRITICAL
    }
    rules {
      text: "Parse XML response to extract: title, authors, year, arxiv_id, primary_category"
      priority: HIGH
    }
    rules {
      text: "Citation key format: LastName{Year}_{arxivid_no_dots}"
      priority: HIGH
    }
    rules {
      text: "Preserve version suffix in URL when citing a specific version read"
      priority: NORMAL
    }
    examples {
      label: "fetch and generate bibtex"
      language: "bash"
      code: "curl -s \"https://export.arxiv.org/api/query?id_list=1706.03762\" | python3 -c \"\nimport sys, xml.etree.ElementTree as ET\nns = {'a': 'http://www.w3.org/2005/Atom', 'arxiv': 'http://arxiv.org/schemas/atom'}\nroot = ET.parse(sys.stdin).getroot()\nentry = root.find('a:entry', ns)\ntitle = entry.find('a:title', ns).text.strip().replace('\\n', ' ')\nauthors = ' and '.join(a.find('a:name', ns).text for a in entry.findall('a:author', ns))\nyear = entry.find('a:published', ns).text[:4]\nraw_id = entry.find('a:id', ns).text.strip().split('/abs/')[-1]\nlast_name = entry.find('a:author', ns).find('a:name', ns).text.split()[-1]\nprint(f'@article{{{last_name}{year}_{raw_id.replace(\\\".\\\", \\\"\\\")},')\nprint(f'  title = {{{title}}},')\nprint(f'  author = {{{authors}}},')\nprint(f'  year = {{{year}}},')\nprint(f'  eprint = {{{raw_id}}},')\nprint(f'  url = {{https://arxiv.org/abs/{raw_id}}}')\nprint('}')\n\""
    }
}
actions {
  id: "semantic_scholar"
  description: "Use Semantic Scholar API for citations, references, and author profiles"
  trigger_phrases: "who cited"
  trigger_phrases: "citations of"
  trigger_phrases: "references from"
  trigger_phrases: "paper impact"
  trigger_phrases: "citation count"
    rules {
      text: "Semantic Scholar: free, 1 req/sec without API key, 100/sec with key"
      priority: HIGH
    }
    rules {
      text: "Paper details: GET /graph/v1/paper/arXiv:{id}?fields=title,authors,citationCount"
      priority: HIGH
    }
    rules {
      text: "Citations OF a paper: GET /graph/v1/paper/arXiv:{id}/citations"
      priority: HIGH
    }
    rules {
      text: "References FROM a paper: GET /graph/v1/paper/arXiv:{id}/references"
      priority: HIGH
    }
    rules {
      text: "Search: GET /graph/v1/paper/search?query=...&fields=title,authors,year,citationCount"
      priority: NORMAL
    }
    rules {
      text: "Recommendations: POST /recommendations/v1/papers/ with positivePaperIds"
      priority: NORMAL
    }
    rules {
      text: "Useful fields: title, authors, year, abstract, citationCount, referenceCount, influentialCitationCount, isOpenAccess, externalIds"
      priority: NORMAL
    }
    data {
      key: "api_base"
      string_value: "https://api.semanticscholar.org/graph/v1"
    }
    data {
      key: "rate_limit"
      string_value: "1 req/sec (no key), 100 req/sec (with key)"
    }
}

guardrails {
  text: "Never generate BibTeX from memory — always fetch programmatically"
  scope: ALWAYS
}

guardrails {
  text: "Respect arXiv rate limit of ~1 request per 3 seconds"
  scope: ALWAYS
}

guardrails {
  text: "Check for withdrawn papers before treating results as valid"
  scope: ALWAYS
}

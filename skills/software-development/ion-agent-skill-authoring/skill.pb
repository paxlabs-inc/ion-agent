meta {
  name: "ion-agent-skill-authoring"
  version: "1.1.0"
  summary: "Author in-repo SKILL.md files — frontmatter, validator, structure, and writing-quality principles."
  author: "Ion Agent"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "skill authoring"
  keywords: "skill.md"
  keywords: "create skill"
  keywords: "write skill"
  keywords: "skill structure"
  keywords: "frontmatter"
  intents: "create_skill"
  intents: "edit_skill"
  intents: "validate_skill"
  patterns: "(create|write|author|edit) .*(skill|SKILL.md)"
  patterns: "skill .*(structure|frontmatter|validation|authoring)"
}

requires {
  tools {
    name: "write_file"
    required: true
  }
  tools {
    name: "terminal"
    required: false
  }
}

provides {
  capabilities: "skill_authoring"
  output_types: "SKILL.md"
}

actions {
  id: "author_skill"
  description: "Create or edit an in-repo SKILL.md file"
  trigger_phrases: "create a skill"
  trigger_phrases: "write a skill"
  trigger_phrases: "author skill.md"
  trigger_phrases: "new skill"
    rules {
      text: "Frontmatter must start with --- as first bytes (no leading blank line) and close with \\n---\\n before body"
      priority: CRITICAL
    }
    rules {
      text: "name field required, lowercase + hyphens, <=64 chars; description required, <=1024 chars, starts with 'Use when'"
      priority: CRITICAL
    }
    rules {
      text: "Every in-repo skill should have: version, author, license, metadata.ion.{tags, related_skills}"
      priority: HIGH
    }
    rules {
      text: "Peer skills in software-development/ sit at 8-14k chars — aim for that range; split bulky content into references/"
      priority: HIGH
    }
    rules {
      text: "Each ordered step must have a checkable completion criterion"
      priority: HIGH
    }
    rules {
      text: "Structure: Title, Overview, When to Use, body sections, Common Pitfalls, Verification Checklist"
      priority: NORMAL
    }
    rules {
      text: "Use strong leading words (tight loop, tracer bullet, root cause) over long explanations"
      priority: NORMAL
    }
    rules {
      text: "Prune no-op prose — 'be careful', 'be thorough', 'use best practices' rarely change model behavior"
      priority: NORMAL
    }
    data {
      key: "directory_pattern"
      string_value: "skills/<category>/<skill-name>/SKILL.md"
    }
    data {
      key: "size_limits"
      map_value {
        entries {
          key: "description"
          string_value: "<=1024 chars"
        }
        entries {
          key: "name"
          string_value: "<=64 chars"
        }
        entries {
          key: "total_file"
          string_value: "<=100000 chars"
        }
      }
    }
    data {
      key: "categories"
      list_value {
        items {
          string_value: "autonomous-ai-agents"
        }
        items {
          string_value: "creative"
        }
        items {
          string_value: "data-science"
        }
        items {
          string_value: "devops"
        }
        items {
          string_value: "software-development"
        }
        items {
          string_value: "social-media"
        }
        items {
          string_value: "productivity"
        }
        items {
          string_value: "research"
        }
      }
    }
    examples {
      label: "peer-matched frontmatter"
      language: "yaml"
      code: "---\nname: my-skill-name\ndescription: Use when <trigger>. <one-line behavior>.\nversion: 1.1.0\nauthor: Ion Agent\nlicense: MIT\nmetadata:\n  ion:\n    tags: [short, descriptive, tags]\n    related_skills: [other-skill]\n---"
    }
}
actions {
  id: "validate_skill"
  description: "Validate an existing SKILL.md file against constraints"
  trigger_phrases: "validate skill"
  trigger_phrases: "check skill.md"
  trigger_phrases: "verify skill"
    rules {
      text: "Check content.startswith('---') — any leading whitespace or BOM fails validation"
      priority: CRITICAL
    }
    rules {
      text: "Verify name <=64 chars, description <=1024 chars, total <=100000 chars"
      priority: HIGH
    }
    rules {
      text: "Confirm body exists after closing ---"
      priority: HIGH
    }
    rules {
      text: "Check related_skills references resolve in-repo"
      priority: NORMAL
    }
    examples {
      label: "local validation script"
      language: "python"
      code: "import yaml, re, pathlib\ncontent = pathlib.Path(\"skills/<cat>/<name>/SKILL.md\").read_text()\nassert content.startswith(\"---\")\nm = re.search(r'\\n---\\s*\\n', content[3:])\nfm = yaml.safe_load(content[3:m.start()+3])\nassert \"name\" in fm and \"description\" in fm\nassert len(fm[\"description\"]) <= 1024\nassert len(content) <= 100_000"
    }
}

guardrails {
  text: "In-repo skills use write_file + git add, NOT skill_manage(action='create')"
  scope: ALWAYS
}

guardrails {
  text: "No-op prose is forbidden — every line must change agent behavior or be deleted"
  scope: ALWAYS
}

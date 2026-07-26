---
name: jamdesk-docs
description: Create and manage documentation sites powered by Jamdesk — CSS variable schema, navigation configuration, docs.json structure, bulk docs rewrite workflows, brand token integration, llms.txt for AI discoverability.
triggers:
  - jamdesk
  - docs site
  - documentation website
  - docs.json
  - developer documentation
  - API docs
  - docs rewrite
---

# Jamdesk Documentation Sites

Construct, maintain, and perform bulk rewrites on documentation sites built with Jamdesk.

## When to use

- Creating a new documentation site for a product or API
- Refreshing outdated docs to align with current source code
- Applying brand design tokens to a Jamdesk site
- Reorganizing docs navigation (e.g., splitting consumer and developer sections)
- Performing bulk content operations across numerous .mdx files

## docs.json structure

Jamdesk sites are driven by a `docs.json` file at the project root. Key fields:

```json
{
  "$schema": "https://jamdesk.com/docs.json",
  "name": "Product Name",
  "theme": "jam",
  "colors": { "primary": "#hex", "light": "#hex", "dark": "#hex" },
  "appearance": { "default": "dark", "strict": true },
  "api": {
    "openapi": ["/openapi/spec.yaml"],
    "examples": { "languages": ["curl", "python", "javascript", "go"] }
  },
  "navbar": { "primary": { "type": "button", "label": "...", "href": "..." } },
  "navigation": { "tabs": [ ... ] },
  "favicon": "url",
  "logo": "path"
}
```

### Navigation structure

The hierarchy is: Tabs > Groups > Pages. Each page references an `.mdx` file path (without extension):

```json
{
  "navigation": {
    "tabs": [
      {
        "tab": "Documentation",
        "icon": "book-open",
        "groups": [
          {
            "group": "Get Started",
            "icon": "rocket",
            "pages": ["introduction", "quickstart", "concepts"]
          }
        ]
      }
    ]
  }
}
```

**Consumer vs Developer split pattern:**
- **Documentation tab**: Get Started, User Guide, FAQ (targeted at general users and billing)
- **Developer tab**: Architecture, MCP & Tools, Writing Docs, Components, Build & Deploy, API Reference, Developer FAQ (targeted at developers and operations)
- **Reference tab**: In-depth technical per-module docs (one group per module)
- **For Agents tab**: llms.txt, sitemap, agent manifest

## CSS variable schema

Jamdesk exposes CSS variables under `:root` for light mode and `[data-theme="dark"]` for dark mode. Place a `style.css` file in the project root (same directory as `docs.json`) — no configuration entry is required.

### Required variables

```css
:root {
  /* Typography */
  --font-family-sans: 'Font', system-ui, sans-serif;
  --font-family-mono: 'MonoFont', monospace;

  /* Layout */
  --content-max-width: 900px;
  --sidebar-width: 280px;

  /* Radius */
  --radius-sm: 4px;
  --radius-md: 8px;
  --radius-lg: 12px;

  /* Core colors (dark mode as default) */
  --color-background: #0a0a0a;
  --color-text: #fafafa;
  --color-text-muted: #a3a3a3;
  --color-border: #262626;
  --color-code-bg: #171717;
  --color-primary: #hex;
  --color-primary-subtle: #hex24;  /* with alpha */
}
```

For dark-only sites (no light mode available), define values directly in `:root` AND duplicate them in `[data-theme="dark"]` as a safety measure.

### Brand token mapping pattern

When the product maintains its own design tokens, map them onto Jamdesk's schema:

| Jamdesk var | Maps from |
|---|---|
| `--color-background` | surface base / background |
| `--color-text` | foreground / text-primary |
| `--color-text-muted` | muted-foreground / text-tertiary |
| `--color-border` | border (medium weight) |
| `--color-code-bg` | card / raised surface |
| `--color-primary` | single accent color |
| `--color-primary-subtle` | accent at ~14% alpha |

### Component targeting

```css
[data-component="card"] { ... }
[data-component="code-block"] { ... }
[data-component="tabs"] [data-state="active"] { ... }
[data-callout="note"] { ... }
[data-callout="warning"] { ... }
[data-callout="danger"] { ... }
```

### External fonts

```css
@import url('https://fonts.googleapis.com/css2?family=Font:wght@400;500;600;700&display=swap');
```

## Bulk docs rewrite workflow

For large-scale rewrites (50+ files), leverage parallel subagents:

1. **Clone the source repository** — the actual codebase that the docs describe
2. **Inspect the architecture** — review READMEs, INDEX.md files, and key source files (first 60-100 lines)
3. **Apply bulk mechanical fixes first** — use `execute_code` for regex operations (em-dash removal, formatting) across all files simultaneously
4. **Dispatch subagents in batches of 3** — each subagent reads source code alongside current docs and rewrites its assigned pages
5. **Update docs.json navigation** — restructure once content is finalized
6. **Apply style.css** — brand tokens come last
7. **Verify** — confirm all nav pages exist, no orphans remain, no banned characters, tokens are present, valid JSON

### Subagent dispatch pattern

Organize work by logical clusters, not alphabetically:
- Batch 1: Consumer pages (intro, quickstart, user-guide, FAQ)
- Batch 2: Developer pages (architecture, module deep-dives)
- Batch 3: API reference pages
- Batch 4+: Reference docs (split by module, 3 subagents per batch)

Each subagent requires:
- Paths to source code files for reading context
- Paths to current doc files to be rewritten
- Explicit rules (no em dashes, maintain mdx format, etc.)

## AI discoverability

Contemporary docs sites should incorporate:
- **`llms.txt`** — a machine-readable doc index allowing AI agents to discover pages
- **MCP server for docs** — enabling AI tools to query docs programmatically
- **Embedded AI chat** — a context-aware assistant
- **`.md` suffix on any URL** — returns a raw markdown version

## Product framing in docs

When rewriting docs for a product that has a default agent/interface (such as Neo for Matrix):
- **Lead with the product's primary entry point**, not the platform abstraction. "Neo is the default agent" > "Matrix is a cognition layer"
- **Avoid buzzword stacking** in descriptions. "Agent framework that takes LLMs past chat into execution" > "cognition layer built for the Machine Economy Vision"
- **Factual chain/platform references are acceptable** (Chain ID, token names, settlement rails) — just don't lead with them
- When the user says "read the source code README", the README's own framing takes precedence — but adapt it to lead with the user-facing component, not the infrastructure
- If the user corrects framing ("remove X wording", "Neo is the main focus"), perform a full scan for all variants of the problematic phrasing across every file before committing

## Verification checklist

After any bulk docs rewrite, execute this verification pattern:

```python
import json, os

docs_dir = '/path/to/docs'

# 1. docs.json valid JSON
with open(os.path.join(docs_dir, 'docs.json')) as f:
    config = json.load(f)

# 2. All nav pages exist as .mdx files
nav = config['navigation']['tabs']
page_refs = set()
for tab in nav:
    for group in tab.get('groups', []):
        for page in group.get('pages', []):
            page_refs.add(page + '.mdx')
            assert os.path.exists(os.path.join(docs_dir, page + '.mdx')), f"MISSING: {page}"

# 3. No orphan .mdx files (not in nav)
all_mdx = set()
for root, dirs, files in os.walk(docs_dir):
    for f in files:
        if f.endswith('.mdx'):
            all_mdx.add(os.path.relpath(os.path.join(root, f), docs_dir))
orphans = all_mdx - page_refs  # subtract known exceptions like openapi-example
assert not orphans, f"Orphans: {orphans}"

# 4. Zero em dashes (or other banned chars)
for root, dirs, files in os.walk(docs_dir):
    for f in files:
        if f.endswith(('.mdx', '.md')):
            with open(os.path.join(root, f)) as fh:
                assert '\u2014' not in fh.read(), f"Em dash in {f}"

# 5. style.css tokens present (whitespace-tolerant check)
with open(os.path.join(docs_dir, 'style.css')) as f:
    css = f.read()
assert '--color-background' in css
assert '--color-primary' in css
```

## Pitfalls

- Jamdesk applies custom CSS globally — use specific selectors to prevent conflicts
- Some internal Jamdesk layout styles rely on `!important` — you may need the same to override them
- Multiple `.css` files in the project root are merged alphabetically by filename
- `jamdesk dev` applies CSS changes on browser refresh; the published site applies them on the next build
- Pages referenced in docs.json navigation MUST have corresponding `.mdx` files or the build fails silently
- Lengthy file names in reference/ paths (common for Deus/LayerX) work but appear awkward — consider shorter names for new docs
- When cloning private repos for docs work, git push requires separate credential setup (gh auth login or SSH keys)
---
name: research-publication-site
description: "Multi-page research publication site: auto-parse academic .txt papers into styled HTML, paper listing with category filters, individual paper pages, clean academic reading layout. Companion to premium-website for research-lab contexts."
version: 1.0.0
author: Ion Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  ion:
    tags: [website, research, academic, papers, publications, multi-page, anti-slop, typography]
    related_skills: [premium-website, claude-design, popular-web-designs]
---

# Research Publication Site Builder

Use this skill when the user wants a **research lab / publication site** — a multi-page site that hosts academic papers, research notes, or technical reports. This is a specialized variant of `premium-website` optimized for text-heavy academic content rather than product marketing.

**Trigger phrases:** "research site", "publication site", "academic site", "papers site", "research lab website", "host our papers", "research docs site".

## When to Use This vs premium-website

| Skill | Use when |
|---|---|
| **research-publication-site** (this one) | Content is academic papers/reports; primary value is reading the research |
| **premium-website** | Content is product/company marketing; primary value is selling or explaining a product |
| **claude-design** | Single-page prototype or one-off landing |

These compose: use `premium-website` for the shared CSS/JS architecture patterns, and this skill for the paper-specific content layer.

## Architecture

```
site/
├── index.html              # Home — hero, thesis, featured paper, research areas
├── research.html           # All papers listing with category filters
├── about.html              # Lab mission, principles, team, contact
├── paper-<slug>.html       # One page per paper (auto-generated)
├── css/style.css           # Shared design system
└── js/main.js              # Scroll reveals, nav, filters, mobile menu
```

## Auto-Parsing Academic Papers

The core technique: read `.txt` papers and convert them to structured HTML automatically.

### Regex Patterns for Academic Structure

```python
import re

# Section headers: "I. INTRODUCTION", "II. BACKGROUND", etc.
content = re.sub(
    r'^([IVX]+\.\s+[A-Z][A-Z\s]+)$',
    r'\n<h3 class="paper-section">\1</h3>',
    content, flags=re.MULTILINE
)

# Subsection headers: "A. Scaling Laws", "B. Related Work", etc.
content = re.sub(
    r'^([A-Z]\.\s+[A-Z].+)$',
    r'\n<h4 class="paper-subsection">\1</h4>',
    content, flags=re.MULTILINE
)

# Special labels: "ABSTRACT", "INDEX TERMS"
content = re.sub(
    r'^(ABSTRACT|Abstract|INDEX TERMS|Index Terms)$',
    r'\n<h3 class="paper-section">\1</h3>',
    content, flags=re.MULTILINE
)
```

### Line-Number Stripping

When reading files via `read_file()`, output includes `LINE_NUM|content` format. Strip with:

```python
lines = []
for line in content.split('\n'):
    m = re.match(r'^\d+\|(.*)$', line)
    if m:
        lines.append(m.group(1))
    else:
        lines.append(line)
clean = '\n'.join(lines)
```

### HTML Wrapping

After section detection, wrap remaining non-empty lines in `<p>` tags:

```python
lines = content.split('\n')
result = []
for line in lines:
    stripped = line.strip()
    if not stripped:
        result.append('')
    elif stripped.startswith('<h3') or stripped.startswith('<h4'):
        result.append(stripped)
    else:
        result.append(f'<p>{stripped}</p>')
html_content = '\n'.join(result)
```

## Paper Page Template

Each paper page needs: Nav (shared), page header with back link, title/authors/affiliation, full paper content with auto-parsed sections, citation block, CTA, footer. A ready-to-use HTML template is below. Fill in the `{placeholders}` and inject parsed content. Key differences from a marketing page: no `.reveal` on the paper content wrapper (renders immediately), `--text-primary` for body text (not `--text-secondary`), mobile responsive breakpoints at 768px.

### Key CSS for Paper Pages

**IMPORTANT:** Paper body text MUST use `--text-primary` (not `--text-secondary`). On dark backgrounds, `--text-secondary` (#8a8784) is too low contrast for extended reading. Use `--text-secondary` only for metadata/citations, never for paper body paragraphs.

```css
.paper-content { max-width: var(--max-w-narrow); margin: 0 auto; padding: 0 var(--gutter); }
.paper-content p { font-size: 0.95rem; color: var(--text-primary); line-height: 1.85; margin-bottom: 1rem; }
.paper-content .paper-section { font-family: var(--font-display); font-size: 1.4rem; font-weight: 400; color: var(--text-primary); margin: 3rem 0 1rem; }
.paper-content .paper-subsection { font-family: var(--font-display); font-size: 1.15rem; font-weight: 400; color: var(--text-primary); margin: 2rem 0 0.75rem; }
.paper-abstract { font-size: 0.92rem; padding: 2rem; background: var(--bg-1); border-left: 2px solid var(--accent); margin: 2rem 0; }
.paper-cite { font-family: var(--font-mono); font-size: 0.72rem; color: var(--text-secondary); padding: 1.5rem; background: var(--bg-1); border: 1px solid rgba(255,255,255,0.04); border-radius: 4px; margin-top: 3rem; }
.paper-back { display: inline-flex; align-items: center; gap: 0.5rem; font-family: var(--font-mono); font-size: 0.75rem; color: var(--text-tertiary); letter-spacing: 0.05em; text-transform: uppercase; margin-bottom: 2rem; transition: color 0.25s; }

/* Mobile: tighten paper typography */
@media (max-width: 768px) {
  .paper-content p { font-size: 0.9rem; }
  .paper-content .paper-section { font-size: 1.2rem; margin: 2rem 0 0.75rem; }
  .paper-content .paper-subsection { font-size: 1rem; }
  .paper-abstract { padding: 1.25rem; }
}
```

### Paper Page Template (HTML skeleton)

```html
<header class="page-header">
  <div class="page-header-inner">
    <a href="research.html" class="paper-back">← All Research</a>
    <p class="section-label">Lab Name</p>
    <h1>{title}</h1>
    <p style="margin-top:1rem; font-size:0.92rem; color:var(--text-tertiary); font-family:var(--font-mono);">
      {authors} · {affiliation}
    </p>
  </div>
</header>

<section class="page-pad">
  <div class="paper-content">
    {parsed_html_content}
    <div class="paper-cite">
      <strong>Citation:</strong> {authors}. "{title}." {lab}, {year}.
    </div>
  </div>
</section>
```

**Do NOT use `.reveal` on the paper content wrapper.** Paper pages are long; the IntersectionObserver may not trigger for content below the fold, leaving text invisible. The page header already handles the entrance animation — paper body should render immediately.

## Research Listing Page

### Category Filter System

Group papers by research area. Use data attributes + JS filtering:

```html
<div class="filter-bar">
  <button class="filter-btn active" data-cat="all">All</button>
  <button class="filter-btn" data-cat="area-a">Area A</button>
  <button class="filter-btn" data-cat="area-b">Area B</button>
</div>

<a href="paper-slug.html" class="paper-card" data-cat="area-a">
  <span class="paper-num">01</span>
  <div class="paper-body">
    <h3>Title with <em>emphasis</em></h3>
    <p>Abstract excerpt...</p>
    <div class="paper-meta">
      <span class="paper-tag">Tag1</span>
      <span class="paper-tag">Tag2</span>
    </div>
  </div>
  <span class="paper-arrow">→</span>
</a>
```

### Filter JS

```js
const filterBtns = document.querySelectorAll('.filter-btn');
const paperCards = document.querySelectorAll('.paper-card');
filterBtns.forEach(btn => {
  btn.addEventListener('click', () => {
    filterBtns.forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    const cat = btn.dataset.cat;
    paperCards.forEach(card => {
      card.style.display = (cat === 'all' || card.dataset.cat === cat) ? '' : 'none';
    });
  });
});
```

### CSS for Paper Cards

```css
.paper-card { display: grid; grid-template-columns: 180px 1fr auto; gap: 2.5rem; align-items: start; padding: 2.5rem 0; background: var(--bg-0); transition: background 0.3s; text-decoration: none; color: inherit; }
.paper-card:hover { background: var(--bg-1); }
.paper-num { font-family: var(--font-mono); font-size: 0.72rem; color: var(--text-tertiary); }
.paper-body h3 { font-family: var(--font-display); font-size: 1.35rem; font-weight: 400; }
.paper-tag { font-family: var(--font-mono); font-size: 0.62rem; color: var(--accent); padding: 0.25rem 0.6rem; border: 1px solid var(--accent-dim); border-radius: 3px; }
.paper-arrow { font-size: 1.2rem; color: var(--text-tertiary); transition: color 0.25s, transform 0.25s; }
.paper-card:hover .paper-arrow { color: var(--accent); transform: translateX(4px); }
@media (max-width: 1024px) { .paper-card { grid-template-columns: 1fr; } .paper-arrow { display: none; } }
```

## Home Page Patterns for Research Sites

### Featured Paper with Sidebar

```html
<div class="paper-featured">  <!-- grid: 1.2fr 1fr -->
  <div class="paper-featured-content">
    <span class="card-tag">Category</span>
    <h3>Featured paper title</h3>
    <p>Abstract excerpt...</p>
    <a href="paper-slug.html" class="btn-primary btn-sm">Read paper</a>
  </div>
  <div class="paper-featured-sidebar">
    <p class="section-label">Also recent</p>
    <div class="mini-paper">
      <h4>Other paper title</h4>
      <p>Short description</p>
    </div>
  </div>
</div>
```

### Research Areas Grid

Use a topics/areas grid on the home page to let visitors navigate by research theme rather than chronologically. Each block shows the area name, description, and paper count.

## Workflow

1. **Gather papers** — read all `.txt` files from the source directory; extract titles, authors, abstracts
2. **Define categories** — group papers into 4-7 research areas based on content
3. **Build CSS** — extend the base design system with paper-specific styles (or use `premium-website`'s CSS as base)
4. **Build shared JS** — add filter logic to the base JS
5. **Build index.html** — hero, thesis quote, featured paper, research areas grid
6. **Build research.html** — filterable paper listing
7. **Build about.html** — lab mission, principles, team, contact
8. **Generate paper pages** — loop through papers, auto-parse content, apply template
9. **Verify** — all pages serve 200, all links resolve, filters work
10. **Serve** — `python3 -m http.server` from the site directory

## Pitfalls

- **Line-number stripping is required** — `read_file()` returns `LINE_NUM|content` format. Always strip before parsing.
- **execute_code can't read local files with relative paths** — when the working directory is `/`, paths like `data/papers/file.txt` fail. Use absolute paths (`/data/papers/file.txt`) or fall back to `terminal()` for file operations.
- **Don't manually write all paper pages** — use `execute_code` to loop through papers and auto-generate. Manual writing of 14+ pages is error-prone and slow.
- **Paper content has special characters** — ampersands, angle brackets in math notation. Always HTML-escape content before injecting into templates: `content.replace('&', '&').replace('<', '<').replace('>', '>')`
- **Academic formatting varies** — some papers use Roman numeral sections (I., II.), some use alphabetic subsections (A., B.), some have both. The regex patterns handle both; verify on a sample before batch-processing.
- **Filter bar needs enough categories** — 4-7 is the sweet spot. Fewer than 4 makes filters pointless; more than 7 clutters the UI.
- **Paper slug generation** — derive from the title, not the filename. Slugs should be human-readable: `paper-intent-compiler.html`, not `paper-02_intent_compiler.html`.
- **Mobile responsive for paper pages** — paper pages need explicit `@media (max-width:768px)` styles: smaller section headers (`1.2rem`), tighter body text (`0.9rem`), reduced abstract padding (`1.25rem`). The shared CSS breakpoints handle cards/grids but not paper-specific typography.
- **Category filter mobile** — filter buttons need smaller font/padding at 768px to avoid wrapping. Use `font-size:0.62rem; padding:0.3rem 0.6rem` for `.filter-btn` on mobile.
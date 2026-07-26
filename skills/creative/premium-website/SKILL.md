---
name: premium-website
description: "Multi-page premium websites: shared CSS/JS architecture, brand asset integration, anti-slop design principles, editorial typography, professional page structure."
version: 1.0.0
author: Ion Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  ion:
    tags: [website, multi-page, premium, design, anti-slop, brand, typography, editorial, corporate, production]
    related_skills: [claude-design, sketch, popular-web-designs, architecture-diagram, research-publication-site]
---

# Premium Website Builder

Use this skill when the user needs a **production-grade, multi-page website** — not a single-file prototype (→ `claude-design`) or throwaway mockup (→ `sketch`). This covers corporate sites, product sites, portfolio sites, and org websites requiring multiple pages with a shared, consistent design system.

**Trigger phrases:** "build me a website", "create our company site", "make a proper website", "multi-page site", "professional website", "org website", "corporate site".

## Decision Table

| Skill | Deliverable | Use when |
|---|---|---|
| **premium-website** (this one) | Multi-page site with shared CSS/JS, nav, footer, brand assets | Company/product site, 3+ pages, production intent |
| **claude-design** | Single self-contained HTML file | One-off landing page, deck, prototype, component lab |
| **sketch** | 2-3 disposable HTML variants | Exploring design direction before committing |
| **popular-web-designs** | CSS values from known brands | Matching Stripe/Linear/Vercel aesthetic |

These compose: use `popular-web-designs` for visual vocabulary, `claude-design` for design taste rules, and `premium-website` for multi-page architecture. For research-lab or publication sites hosting academic papers, use `research-publication-site` instead — it adds auto-parsing of `.txt` papers, category filters, and paper page templates on top of the same CSS/JS architecture.

## Anti-Slop Design Principles

Every site built with this skill MUST follow these rules. The full pattern catalog lives in `references/anti-slop-patterns.md` — load it when building or auditing. When integrating AI-generated illustrations, load `references/ai-art-integration.md` for the knock-out/roundel pipeline.

### Typography
- NEVER default to Inter/Poppins/Roboto as the only font
- Use editorial serif for display (Instrument Serif, Clash Display, PP Editorial New) + modern sans for body (Inter at weight 300, Satoshi, General Sans)
- Monospace for specs, tags, code, metrics (JetBrains Mono)
- Dramatic hierarchy: display at `clamp(3rem, 7vw, 6.5rem)` down to 0.7rem mono labels
- Mix serif + sans-serif for tension

### Color
- NEVER use purple-to-blue gradients as the default accent
- Choose a distinctive, muted accent: warm copper (#c8b4a0), dusty olive, muted rose
- Near-black (#0a0a0a) not pure #000; near-white (#e8e6e3) not pure #fff
- Dark mode elevation via surface layers (bg-0 → bg-1 → bg-2), not shadows
- Desaturate accent colors ~20 points for dark mode

### Layout
- NEVER default to symmetric 3-column feature cards
- Use asymmetric grids (golden ratio 1:1.618, 60/40, 70/30)
- Break the grid intentionally — overlapping elements, bleeds, offset placement
- Vary section sizes dramatically (one huge, one compact)
- White space as active design element

### Motion
- Scroll reveals via IntersectionObserver (no library needed)
- Staggered children — don't animate everything at once
- Custom easing curves, not `ease-in-out`
- Every animation must justify its existence

### Content
- NEVER use buzzwords: "revolutionize", "cutting-edge", "seamlessly integrate"
- NEVER use AI-slop prose constructions — this user rejects them on sight and explicitly asks for "natural rhythm":
  - "Not X. Y." staccato fragments ("Not autocomplete with a terminal. A colleague.")
  - "not just X — Y" parallelism ("An agent's past is not just stored — it is provable.")
  - aphorism tics: "stops being a metaphor", "never a fabricated success", "talks the best game"
  - em-dash parentheticals as the default punctuation of every sentence — prefer colons and commas
  - identical sentence rhythm paragraph after paragraph; vary length deliberately, let some sentences run long
- Do a dedicated DE-SLOP PASS over all prose before delivery: grep for the patterns (`not just|stops being|never a|—`) and rewrite each hit into a plainer, more varied cadence. Read key sections aloud — if it sounds like a LinkedIn post, rewrite it.
- Specific CTAs, not generic "Get Started" / "Learn More"
- Real copy from the product/company docs
- Distinct voice — technical, editorial, or blunt

### Texture
- SVG grain overlay at 3% opacity (see `references/anti-slop-patterns.md` for the CSS)
- Vary border-radius across elements (4px buttons, 8px code blocks, 3px tags)
- NOT every card with identical shadow

## Architecture Pattern

### Directory Structure
```
site/
├── index.html          # Home — hero, thesis, product overview, latest posts, CTA
├── products.html       # Products — detailed cards, stack diagram, ecosystem
├── developers.html     # Developer hub — code samples, SDKs, network config, docs
├── blog.html           # Blog/news — featured post, chronological list
├── about.html          # About — mission, principles, team, brand assets
├── css/
│   └── style.css       # Shared design system — tokens, components, responsive
├── js/
│   └── main.js         # Shared JS — scroll reveals, nav, active links
└── images/             # Brand assets, illustrations, logos
```

### Shared CSS Design System
The CSS file is the single source of truth. Structure it as:
1. **Reset + Tokens** (CSS custom properties for all colors, fonts, spacing)
2. **Navigation** (fixed, backdrop-blur, scrolled state)
3. **Buttons** (primary filled, ghost outline, size variants)
4. **Sections** (shared padding, inner containers, labels, headings)
5. **Components** (cards, code blocks, stack layers, primitives, blog items)
6. **Animations** (keyframes, .reveal, .stagger)
7. **Responsive** (tablet @1024px, mobile @768px breakpoints)

### Shared JS
Minimal, no dependencies:
- IntersectionObserver for scroll reveals
- Nav scroll state (add `.scrolled` class)
- Smooth scroll for anchor links
- Active nav link highlighting

### Page Templates

**Home (index.html):**
- Hero with editorial headline, subtitle, CTAs
- Optional hero visual: illustration or SVG on the right side of a 2-column grid (`grid-template-columns: 1fr 1fr`). On mobile (≤1024px), visual stacks above text with `order:-1`. Use `<img src="file.svg">` for SVGs — don't inline large SVGs.
- Optional eyebrow label above hero headline — skip if the site identity is already clear from the nav logo
- Metrics/stats bar (live data if available)
- Thesis/philosophy blockquote
- Product overview (2-up cards with images)
- Image band (4 brand images in a row, grayscale → color on hover)
- Primitives/features (2-col grid)
- Latest blog posts (3-up cards)
- CTA section

**Products (products.html):**
- Page header with label + heading
- Product cards (side-by-side, alternating image/content sides)
- Full stack diagram (layered rows: name | description | technology)
- Ecosystem grid (3-col cards)
- CTA

**Developers (developers.html):**
- Page header
- Code sample (terminal-style with syntax highlighting)
- SDK cards (3-col)
- Network config panels (EVM + Cosmos side by side)
- Documentation links
- Protocol primitives
- CTA

**Blog (blog.html):**
- Page header
- Featured post (image + content side by side)
- Blog list (date | content | tag columns)
- Image band
- CTA

**About (about.html):**
- Page header
- Mission blockquote
- What we build (3 cards with images)
- Image band
- Values/principles (asymmetric 2-col: sticky heading + list)
- Company info
- Brand asset gallery (logo variations, character assets)
- CTA

## Brand Asset Integration

When the user provides a brand kit (GitHub repo, folder, or URLs):
1. Download all usable assets (logos, characters, illustrations) via `curl` from raw GitHub URLs
2. Organize into `images/` with descriptive filenames
3. Use logos in nav + footer
4. Use character/illustration images in product cards, about page, feature sections
5. Use brand mark variations in a brand asset gallery section
6. Apply brand colors to the design tokens if provided

Raw GitHub URL pattern: `https://raw.githubusercontent.com/{owner}/{repo}/main/{path}`

## AI-Generated Art Integration

When the user supplies a folder of AI-generated illustrations to weave through the site, use the knock-out + roundel pipeline (full procedure and script in `references/ai-art-integration.md`):
1. **Contact-sheet QA first** — tile all images into one grid, send to the vision model (xAI `grok-4.5` via chat/completions with base64), get one-line subject/color/background description per image. Map subjects to sections BEFORE processing.
2. **Background knock-out** — border-seeded flood fill (numpy + scipy.ndimage.label), tolerance ~34, keeps interior detail. Verify alpha coverage stats per image.
3. **Vision-verify on the site's actual background color** — composite every result onto the page bg, grid them, ask the vision model CLEAN vs BOXED per image.
4. **Roundel fallback for BOXED images** — composite onto a deep-ink circle with a thin accent ring; a deliberate "print" treatment that looks intentional. Never ship a haloed cutout.
5. Rename outputs descriptively (`art-gardener.png`, not `mrq1pe5f....png`) and integrate with a shared `.art-figure` / `.art-inline` / `.art-strip` CSS system (see the reference file for the exact CSS block).

## Shared Glyph Library (inline SVG symbols)

For recurring iconography (machine-seal / orbital / meridian / helix style glyphs), define all `<symbol>`s once in a `glyphs.svg` file and inject it per page:
```html
<div id="glyph-lib"></div>
<script>
  // glyphs.svg is a same-origin static asset we author — safe to inline
  fetch('glyphs.svg').then(r => r.text()).then(t => {
    document.getElementById('glyph-lib').innerHTML = t;
  });
</script>
```
Then reference anywhere with `<svg viewBox="..."><use href="#glyph-name"/></svg>`. `<use>` cannot reach an external SVG file without inlining, and inlining avoids CORS issues on file:// — this is the reliable pattern. Keep glyph strokes at 1px in `currentColor` so sections can tint them via CSS `color`.

## Workflow

1. **Gather context** — brand docs, GitHub repos, product descriptions, existing copy
2. **Download brand assets** — images, logos, illustrations from provided sources
3. **Build shared CSS** — design tokens, components, responsive breakpoints
4. **Build shared JS** — scroll reveals, nav behavior
5. **Build pages** — start with index.html, then products, developers, blog, about
6. **Verify** — file structure, file sizes, all pages exist and link correctly
7. **De-slop pass** — grep all prose for AI-slop patterns and rewrite (see Content rules)
8. **Deliver** — default to what the user asked for. If they say "don't serve, put into zip", package with `python3 -c "import shutil; shutil.make_archive('/data/name','zip','/data','folder')"` (the `zip` binary may not exist) and just report the path — this user wants results, not step narration. Otherwise serve with `python3 -m http.server` and report the URL.

## Pitfalls

- **Python f-strings with HTML/CSS/JS** — curly braces `{}` in CSS (`:root { --var: val; }`) and JS (`{ key: val }`) conflict with Python f-string syntax. Use `write_file()` directly instead of f-string interpolation when the content has many curly braces.
- **Don't make every page look the same** — vary the layouts: some pages use asymmetric grids, some use full-width bands, some use sticky sidebars.
- **Image sizing** — always set `object-fit: cover` and explicit `aspect-ratio` on card images to prevent layout shift.
- **Nav consistency** — every page must have the same nav with the same links. Highlight the active page.
- **Footer consistency** — same footer on every page. Include product links, developer links, company links.
- **Don't forget mobile** — every layout must collapse gracefully. Test by imagining the page at 375px width.
- **Image downloads** — use `curl -sL` (silent + follow redirects). Check file sizes after download to confirm they're not error pages.
- **`.reveal` on long content** — never use `.reveal` (opacity:0) on page sections that are far below the fold. The IntersectionObserver may not trigger, leaving content invisible. Use `.reveal` only on content near the top of the viewport; for long-form text pages (papers, blog posts), render body content immediately and reserve `.reveal` for the page header only.
- **Text contrast on dark backgrounds** — `--text-secondary` (#8a8784) is too low contrast for body text on dark backgrounds (#0a0a0a). Use `--text-primary` (#e8e6e3) for any text the user needs to read at length. Reserve `--text-secondary` for metadata, captions, and tertiary info.
- **Light "paper edition" of a dark brand kit** — when the brand tokens are dark-only (e.g. Matrix warm charcoal) but the user asks for a light/warm-paper editorial style, INVERT the ladder rather than fighting it: paper ground from the kit's lightest stone (`#f2efeb`), ink from the darkest (`#1b1b19`), keep the single brand accent (sage) but deepen it one step for contrast on light (`#6a8a6d` over `#99bd9c`). Amber stays for sparse warning/highlight use only.
- **Terminal `&` backgrounding is rejected** — `cmd1 & cmd2 & wait` in the terminal tool errors out ("Foreground command uses '&' backgrounding"). Use a sequential `for` loop with `curl -sL` per asset instead; 8 small images download in a few seconds anyway.
- **PIL pixel loops are slow and buggy — use numpy+scipy for image masking** — `im.load()` returns a different accessor for RGBA vs RGB; a hand-rolled BFS over 1M pixels is minutes-slow. `pip install scipy` and use `ndimage.label` on a distance mask (see `references/ai-art-integration.md`).

## File Sizes (Expected)

A well-built 5-page site with shared CSS/JS:
- Each HTML page: 8-12 KB
- Shared CSS: 15-25 KB
- Shared JS: 1-2 KB
- Images: varies (brand assets typically 50KB-2MB each)
- Total (excl images): ~70-80 KB for 5 pages

## Verification Checklist

Before declaring done:
- [ ] All HTML pages exist and are non-empty
- [ ] CSS file exists and loads on every page
- [ ] JS file exists and loads on every page
- [ ] All images exist and are non-zero
- [ ] Nav links point to correct pages on every page
- [ ] Footer is consistent across all pages
- [ ] No broken internal links
- [ ] Responsive breakpoints defined
- [ ] Scroll animations work (reveal/stagger classes present)
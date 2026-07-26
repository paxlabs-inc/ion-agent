---
name: hyperframes
description: Build HTML-driven video compositions including animated title sequences, social media overlays, captioned talking-head edits, audio-reactive graphics, and shader transitions with HyperFrames. HTML serves as the definitive source for video output.
trigger: Activate when the user needs a rendered MP4/WebM from an HTML composition, wants text/logos/charts animated over media, requires audio-synced captions, needs TTS narration, or wants to turn a website into a video.
source: https://hyperframes.heygen.com/introduction
install: npm install -g hyperframes (or use npx hyperframes)
---

# HyperFrames

## Prerequisites

- **Node.js 22+**
- **FFmpeg** — install via `apt install ffmpeg` or `brew install ffmpeg`
- **Chrome dependencies** (Debian/Ubuntu):
  ```bash
  apt install -y unzip libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 libcups2 libdrm2 libxkbcommon0 libatspi2.0-0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libpango-1.0-0 libcairo2 libasound2
  ```
  Headless Chrome will fail with "missing shared libraries" errors if these are absent.

## Project Setup

```bash
npx hyperframes init my-video --non-interactive --example blank
cd my-video
```

Generates: `index.html`, `meta.json`, `package.json`, `hyperframes.json`

## Core Commands

```bash
npx hyperframes lint                              # validate project (pass DIRECTORY, not file)
npx hyperframes preview                           # live browser preview
npx hyperframes render --output out.mp4           # render index.html
npx hyperframes render --composition foo.html --output foo.mp4   # render specific file
npx hyperframes docs <topic>                      # local CLI docs
```

## Composition Rules (Critical)

1. **Root element** must have `data-composition-id`, `data-width`, `data-height`
2. **Every timed element** must include both `class="clip"` AND `data-start`, `data-duration`, `data-track-index`
3. **GSAP timeline** needs `{ paused: true }` and must be registered on `window.__timelines`:
   ```js
   const tl = gsap.timeline({ paused: true });
   tl.from("#title", { opacity: 0, y: 40, duration: 0.8, ease: "power3.out" }, 0.6);
   window.__timelines = window.__timelines || {};
   window.__timelines["your-composition-id"] = tl;
   ```
4. **Sub-composition files must NOT have `data-composition-id`** — this triggers a "multiple root compositions" error
5. **No overlapping clips sharing the same track** — either adjust timing or assign a different `data-track-index`
6. **Deterministic only** — avoid `Date.now()`, `Math.random()`, and network fetches
7. **Self-contained** — use inline styles, inline scripts, and CDN-loaded libraries

## Rendering Individual Compositions

```bash
npx hyperframes render --composition neo-hero.html --output 01-neo-hero.mp4
```

`--composition` takes a path relative to the project directory. There is no `--input` flag.

## Combining Videos with ffmpeg

```bash
cat > concat.txt << 'EOF'
file '01-intro.mp4'
file '02-main.mp4'
EOF
ffmpeg -y -f concat -safe 0 -i concat.txt -c copy combined.mp4
```

## Pitfalls

- **`npx hyperframes lint file.html`** errors with "Not a directory" — instead run `npx hyperframes lint` from the project root
- **`--input` flag does not exist** — use `--composition` as the alternative
- **"Composition has zero duration"** — ensure `data-duration` is set on the root or register a GSAP timeline
- **"Multiple root compositions"** — sub-composition HTML files must not include `data-composition-id`
- **45s timeout** — compositions lacking `window.__timelines` trigger a 45s poll delay; add `data-no-timeline` to skip it
- **Font loading** — HyperFrames retrieves Google Fonts; system fonts (SF Pro, etc.) degrade to Helvetica
- **Chrome not found** — install `unzip` along with Chrome system dependencies (refer to Prerequisites)
- **Static-frame dedup** — compositions using only GSAP (no video/audio) qualify for frame dedup and still render properly

## Tips

- Keep each composition under 10s for rapid iteration; merge with ffmpeg afterward
- Run `npx hyperframes docs gsap` for GSAP-specific animation documentation
- Run `npx hyperframes docs data-attributes` for the complete data-* reference
- Each render worker spawns a separate Chrome process (~256MB RAM)
- Preview with `npx hyperframes preview` before committing to a full render
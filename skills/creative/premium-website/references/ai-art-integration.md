# AI Art Integration Pipeline

Procedure and code for taking a folder of AI-generated illustrations (typically dark/textured backgrounds) and integrating them into a light-background multi-page site. Developed for the PaxLabs site (18 teal-on-dark Grok images onto warm paper #f2efeb).

## Step 1 — Contact-sheet QA

Tile all source images into one grid with index numbers, then ask the vision model (xAI grok-4.5 via chat/completions, base64 data URL) for a one-line description per image: subject, dominant colors, background type (white / dark / textured). Use this to map subjects to page sections BEFORE any processing. A 6x3 grid of 240-256px tiles at JPEG q82 fits comfortably in one vision call.

## Step 2 — Background knock-out

Border-seeded flood fill. Tolerance ~34 on max-channel distance from the corner-sampled background color. Only regions touching the image border are removed, so interior dark detail (shadows, clothing, space scenes) survives.

```python
import numpy as np
from PIL import Image
from scipy import ndimage

def knock_out(path, tol=34):
    im = Image.open(path).convert("RGB")
    a = np.asarray(im).astype(np.int16)
    h, w, _ = a.shape
    corners = np.array([a[0,0], a[0,w-1], a[h-1,0], a[h-1,w-1]])
    bg = corners.mean(axis=0)
    dist = np.abs(a - bg).max(axis=2)
    near_bg = dist <= tol
    lab, _ = ndimage.label(near_bg)
    border = set(np.unique(np.concatenate([lab[0,:], lab[-1,:], lab[:,0], lab[:,-1]]))) - {0}
    bgmask = np.isin(lab, list(border))
    rgba = np.dstack([np.asarray(im), np.where(bgmask, 0, 255).astype(np.uint8)])
    return Image.fromarray(rgba, "RGBA")
```

Requires scipy (`pip install scipy`). Sanity-check results via alpha stats: `(alpha==0).mean()` for transparent fraction and the opaque bounding box — a `transparent=0.00` result means the background wasn't uniform (textured/gradient) and the flood fill found nothing.

## Step 3 — Vision-verify on the real page background

Composite EVERY result onto the site's actual background color, grid them, and ask the vision model to call each CLEAN or BOXED (visible halo/dark rectangle/fringe). Do not trust alpha stats alone — a 50% transparent image can still have an ugly fringe.

## Step 4 — Roundel fallback for BOXED images

Images with textured/gradient backgrounds (starfields, grids, waves) don't flood-fill cleanly. Instead of shipping a halo, composite the full square onto a deep-ink disc with a thin accent ring — it reads as an intentional "print" or "coin" motif:

```python
from PIL import Image, ImageDraw, ImageFilter

def roundel(src, dst, size=1024, ink=(27,27,25,255), ring=(106,138,109,255)):
    im = Image.open(src).convert("RGBA").resize((size,size))
    base = Image.new("RGBA",(size,size),ink); base.alpha_composite(im)
    m = Image.new("L",(size,size),0)
    d = ImageDraw.Draw(m); pad=40
    d.ellipse([pad,pad,size-pad,size-pad], fill=255)
    m = m.filter(ImageFilter.GaussianBlur(6))
    out = Image.new("RGBA",(size,size),(0,0,0,0))
    out.paste(base,(0,0),m)
    rl = Image.new("RGBA",(size,size),(0,0,0,0))
    ImageDraw.Draw(rl).ellipse([pad-6,pad-6,size-pad+6,size-pad+6], outline=ring, width=10)
    out.alpha_composite(rl)
    out.save(dst)
```

Roundels work especially well floated inside editorial text blocks or as section anchors.

## Step 5 — Naming + integration

Rename outputs descriptively (`art-gardener.png`, `art-maze.png`) — never ship `mrq1pe5f0def....png` filenames into HTML. Shared CSS system:

```css
.art-figure { display:flex; flex-direction:column; align-items:center; gap:0.9rem; }
.art-figure img { width:100%; max-width:420px; filter:drop-shadow(0 18px 40px rgba(27,27,25,0.14)); }
.art-figure.roundel img { border-radius:50%; }
.art-figure figcaption { font-family:var(--font-mono); font-size:0.62rem; letter-spacing:0.18em; text-transform:uppercase; color:var(--text-tertiary); }
.art-inline { float:right; width:min(38%,300px); margin:0 0 1.5rem 2.5rem; }
.art-strip { display:flex; justify-content:center; align-items:flex-end; gap:clamp(1.5rem,5vw,5rem); padding:3rem var(--gutter); background:var(--paper-warm); border-top:1px solid var(--rule); border-bottom:1px solid var(--rule); }
.art-strip .art-figure img { max-width:240px; }
@media (max-width:900px){ .art-inline{float:none;width:240px;margin:1rem auto 1.5rem;} .art-strip{flex-wrap:wrap;} }
```

Placement patterns that worked: one art figure overlapping the hero visual (absolute-positioned corner), a 3-piece `.art-strip` between major sections as a visual rest, `.art-inline` floats inside long prose sections, roundels anchoring sticky editorial sidebars. Match subject matter to section meaning (gardener tree -> memory; maze -> routing/registry; boat -> continuity).

## Pitfalls

- PIL `im.load()` returns RGBA tuples only in RGBA mode — convert to RGB first for distance math, and prefer numpy over per-pixel Python loops (minutes vs seconds at 1024px).
- Verify with vision on the ACTUAL page bg color, not white — halos invisible on white become obvious on cream.
- Hero overlap figures need the wrap to be `position:relative` and the art absolutely positioned with explicit width (40-45%) so it doesn't blow out the layout.

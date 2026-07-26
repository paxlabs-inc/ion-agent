# Anti-Slop Design Patterns — Reference

Research-validated catalog of AI-generated website tells and their human-crafted counter-patterns.

---

## AI Slop Hallmarks (Score Against These)

### Color Tells
- Purple-to-blue gradients on everything — #1 AI color signature
- Dark mode + glowing colored box-shadows
- Gradient text (purple-to-cyan, violet-to-fuchsia)
- Cream/beige "tasteful" palette when AI tries to be "elevated"
- Cyan-on-dark as secondary accent

### Typography Tells
- Inter everywhere — default in every AI tool, zero hierarchy
- Single font for everything — no pairing, no personality
- Flat type hierarchy — heading/body nearly same size
- Italic serif display headline — universal AI-startup pattern
- Crushed letter-spacing on headings

### Layout & Structure Tells
- Predictable: Hero → Features (3 cards) → Stats → Testimonials → Pricing → CTA → Footer
- 3-column identical feature cards (icon + heading + sentence × 3)
- Symmetric, centered everything — no visual tension
- Uniform component sizing — identical padding, identical border-radius everywhere
- Side-tab accent cards — thick colored border on one side ("single most recognizable tell")
- Cards inside cards inside cards — five levels of nesting
- Hero metric layout — "Big number, small label, three supporting stats"
- "Trusted by" logo row with opacity-reduced logos

### Copy Tells
- Vague aspirational headlines: "Build the future of work." "Your all-in-one platform."
- Hero eyebrow/pill chip — tiny uppercase label above hero
- Buzzword soup: "revolutionize," "cutting-edge," "seamlessly integrate"
- Generic testimonials with fictional names
- CTAs: "Get Started" or "Learn More" without specificity

### Visual Decoration Tells
- Floating blurred orbs/blobs — animated amorphous purple/fuchsia/cyan shapes
- Glassmorphism everywhere — blur effects as decoration
- Hairline border + wide shadow signature
- Neon text-shadows
- AI-generated illustrations — "slightly too smooth, slightly too symmetrical"
- Bouncing scroll indicators

### Interaction Tells
- Same generic fade-in on every element
- Hover states that do nothing or just `scale(1.05)`
- Same timing function everywhere (`ease-in-out, 0.3s`)
- Floating AI chat widget in bottom-right

---

## Premium Counter-Patterns

### Typography Pairings (2025-2026)
- **Editorial:** Instrument Serif (display) + Inter weight 300 (body) + JetBrains Mono (specs)
- **Modern:** Clash Display (display) + General Sans (body) + IBM Plex Mono (code)
- **Luxury:** PP Editorial New (display) + Satoshi (body) + Space Mono (accents)
- **Technical:** Neue Montreal (display) + Switzer (body) + JetBrains Mono (everything code)

### Color Approaches
- **Warm copper:** #c8b4a0 accent on #0a0a0a background (PaxLabs example)
- **Muted olive:** #8a9a7b accent on near-black
- **Dusty rose:** #c4a0a0 accent — warm but not girly
- **Deep navy:** #0a2463 accent on light (#f5f5f0) — Stripe-style
- **Rule:** pick ONE muted, desaturated accent. Never default to purple/blue.

### Layout Patterns
- **Golden ratio grid:** `grid-template-columns: 1fr 1.618fr` for asymmetric two-column
- **Sticky sidebar:** left column stays fixed while right scrolls (philosophy/principles sections)
- **Image band:** 4 images in a row, grayscale → color on hover, gap:2px
- **Alternating product cards:** image-left/content-right, then content-left/image-right
- **Stack diagram:** single-column rows with name | description | technology columns

### Motion Patterns
- **Scroll reveal:** IntersectionObserver adding `.visible` class, CSS transition handles the rest
- **Stagger children:** `.stagger.visible > *:nth-child(N) { transition-delay: 0.1*N }`
- **Nav shrink:** `.nav.scrolled { padding: 1rem }` on scroll > 100px
- **Custom easing:** `cubic-bezier(0.22, 1, 0.36, 1)` for smooth entrances

### Texture
- SVG feTurbulence noise at 3% opacity, mix-blend-mode: overlay, fixed position, pointer-events: none
- Vary border-radius: 4px buttons, 8px code blocks, 3px tags, 6px logo marks
- Film-grain effect on photography via CSS filters

---

## Sources
- Awwwards case studies (Obys, Bruno Simon, BL/S®, Exo Ape, Adelt)
- Impeccable.style slop detector (46 codified patterns)
- 925Studios AI slop guide
- Designer discussions on X/Twitter, Reddit
- Codrops, Josh W. Comeau, GSAP Showcase

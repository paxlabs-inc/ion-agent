# Social Media Illustration Campaign Pattern

Reusable workflow for generating themed illustration sets with consistent style and brand-fitting captions.

## Content Grid Template

When planning 15-20 posts, map concepts across these categories:

| Category | Example Concepts | Posts |
|----------|-----------------|-------|
| **Identity / Launch** | Hero moment, red pill, key, portal | 2-3 |
| **Product / Features** | Agent working, speed, wallet, tools | 3-4 |
| **Tokenomics / Chain** | Tokens, staking, chain ID, DeFi | 2-3 |
| **Community / Growth** | Onboarding, voice, collaboration | 3-4 |
| **Values / Philosophy** | Security, transparency, decentralization | 3-4 |
| **Vision / Leadership** | Pioneer, momentum, future | 2-3 |

## Prompt Engineering for Consistent Style Sets

When generating a batch of illustrations that must share the same visual DNA:

1. **Extract the style description once** (via vision API on the reference)
2. **Front-load every prompt** with the same style preamble:
   ```
   Hand-drawn sketchy illustration on pure black background. Pale mint-green (#D0E0D0) loose contour lines.
   ```
3. **Vary only the subject and energy** — keep colors, line quality, and background identical
4. **Add "energy" keywords** at the end of each prompt to set the mood:
   - Celebratory, triumphant energy
   - Productive, focused energy
   - Liberation, breaking free energy
   - Security, trust, protection energy
   - Innovation, ideas energy

## Caption Writing Guidelines

- Lead with the visual, not the platform
- Short sentences. Line breaks for rhythm.
- Brand voice: direct, confident, builder-first
- No corporate jargon ("leverage", "synergy", "ecosystem play")
- End with relevant hashtags (3-5 max)
- Platform variations:
  - **X/Twitter**: Full caption + image, thread-eligible
  - **Telegram**: Image + caption, consider splitting long text
  - **Instagram**: Image-first, caption below, hashtags in comments

## Posting Strategy Template

| Phase | Posts | Frequency | Goal |
|-------|-------|-----------|------|
| Launch | Identity posts | Days 1-3 | Hype + recognition |
| Build | Product/feature posts | Days 4-6 | Technical appeal |
| Ecosystem | Tokenomics posts | Days 7-9 | Value prop |
| Community | Growth posts | Days 10-13 | Onboarding |
| Values | Philosophy posts | Days 14-17 | Trust + differentiation |
| Momentum | Vision posts | Days 18-20 | Leadership + future |

## Delivery Format

Save all assets to a single directory:
```
/data/<campaign-name>/
  ├── 01-concept-name.png
  ├── 02-concept-name.png
  ├── ...
  └── social-media-plan.md    # Full plan with captions + strategy
```

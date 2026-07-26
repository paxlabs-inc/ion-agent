# Matrix brand tokens mapped to Jamdesk CSS variables

Source: `paxlabs-inc/matrix-brand-kit/tokens/colors.css`
Target: Jamdesk `style.css`

## Core mapping

| Jamdesk variable | Matrix token | Value | Notes |
|---|---|---|---|
| `--color-background` | `--background` | `#161615` | Warm near-black |
| `--color-text` | `--foreground` | `#e3d9d4` | Cream text |
| `--color-text-muted` | `--muted-foreground` | `#96918e` | Warm gray |
| `--color-border` | `--border` | `#2a2a27` | Warm border |
| `--color-code-bg` | `--card` | `#1b1b19` | Raised surface |
| `--color-primary` | `--pax` | `#99bd9c` | Sage accent (single accent) |
| `--color-primary-subtle` | `--pax-soft` | `#99bd9c24` | Sage at 14% alpha |

## Typography chosen

- **Body**: DM Sans (clean, warm, not Inter)
- **Headings**: Instrument Serif (editorial personality)
- **Code**: JetBrains Mono

## Extended tokens (as custom properties)

```css
/* Surfaces (tonal elevation) */
--surface-base:    #161615;  /* background */
--surface-raised:  #1b1b19;  /* card */
--surface-overlay: #222220;  /* popover */
--surface-hover:   #2a2a27;  /* border level */
--surface-active:  #353530;  /* gray-700 */

/* Borders (weight ladder) */
--border-subtle:   #1b1b19;
--border-default:  #2a2a27;
--border-strong:   #353530;
--border-heavy:    #4a463f;

/* Callout colors */
--color-success:     #99bd9c;  /* sage = success */
--color-warning:     #fe9a00;  /* amber */
--color-destructive: #ef4444;  /* red */
```

## Callout styling

```css
[data-callout="note"]    { border-left-color: var(--color-primary); background: var(--color-primary-subtle); }
[data-callout="warning"] { border-left-color: var(--color-warning); background: #fe9a0014; }
[data-callout="danger"]  { border-left-color: var(--color-destructive); background: #ef444414; }
```

# Design

<!-- impeccable:design-schema 1 -->

## Visual World

**Calm operational.** A "Operate" mode surface — the operator came
to *work* the witsaba stack, not to be persuaded or entertained.
The aesthetic is the look of a well-tuned internal tool: neutral
grounds, a single restrained accent, system typography, generous
spacing, and a top bar. Familiarity is a feature; the operator
should recognize this as a tool the moment it loads.

## Color Strategy

**Restrained.** Neutrals plus one accent. The accent does real
work: it marks the primary action, the current selection, and
state indicators. It does not decorate. No full-bleed color
regions, no gradients, no glass.

| Role | Light | Dark |
|---|---|---|
| Page background | `#f7f7f5` warm-neutral | `#0e1014` near-black |
| Surface (cards, panels) | `#ffffff` | `#161a20` |
| Border, hairline | `#e6e6e1` | `#262b33` |
| Text, primary | `#16181d` | `#e6e8ec` |
| Text, secondary | `#5b6470` | `#9aa3b0` |
| Accent (operational) | `#1f6feb` signal blue | `#4d8df6` |
| State: success / healthy | `#117a3d` | `#3fbf6f` |
| State: warning | `#8a5a00` | `#e0a93a` |
| State: error / unhealthy | `#b3261e` | `#f06e63` |

The accent (signal blue) earns its keep on:
- the brand wordmark
- the primary "open" link on a feature card
- the active nav item underline / left bar
- focus rings

The accent is *not* used for: card icons, large colored regions,
decorative dividers, hover backgrounds (hover uses a tint of the
neutral surface).

## Typography

**One family: the platform system stack.** No display face, no
serif pairing. The product surface is dense and information-heavy;
a tuned sans carries headings, body, labels, and data at every
weight without competing for attention.

```
font-family:
  ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont,
  "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
```

Monospace is used only where it carries information: log lines,
device IDs, version strings, IP addresses.

```
font-family-monospace:
  ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
```

Type scale (rem, fixed — not fluid):

| Token | Size | Line height | Weight | Use |
|---|---|---|---|---|
| `--text-xs` | 0.75 | 1.4 | 500 | eyebrow labels (used sparingly) |
| `--text-sm` | 0.8125 | 1.5 | 400 | metadata, secondary |
| `--text-base` | 0.9375 | 1.55 | 400 | body |
| `--text-md` | 1.0625 | 1.45 | 500 | list rows, table cells |
| `--text-lg` | 1.25 | 1.35 | 600 | card titles |
| `--text-xl` | 1.5 | 1.25 | 600 | section headings |
| `--text-2xl` | 2 | 1.15 | 600 | hero title |

Tracking: `letter-spacing: -0.01em` on `--text-xl` and above.
Body measure: 65–75ch for prose; data tables run denser (up to
120ch) where reading patterns differ.

## Spacing

**Tight groups, generous separation.** A 4px base unit, a 1.5× step.

| Token | Value | Use |
|---|---|---|
| `--space-1` | 0.25rem (4px) | between paired icon and label |
| `--space-2` | 0.5rem (8px) | tight groups (label + value rows) |
| `--space-3` | 0.75rem (12px) | between sibling metadata |
| `--space-4` | 1rem (16px) | card padding (start), list gaps |
| `--space-5` | 1.5rem (24px) | between content blocks within a card |
| `--space-6` | 2rem (32px) | card padding (generous), between cards |
| `--space-7` | 3rem (48px) | hero / first viewport breathing room |

Rule: **more space above a heading than below it.** Headings earn
their prominence by what precedes them, not by what follows.

## Layout

**Top bar + content.** Standard operator-tool navigation. The top
bar is fixed-height (3.5rem), holds the brand, the primary nav, and
a status indicator on the right (Postgres + workers health). The
content region below is a single column, max-width 72rem, with
generous horizontal padding that collapses to 1rem on narrow
viewports.

Breakpoints (mobile-first):

| Name | Min width | Change |
|---|---|---|
| (default) | 0 | single column, nav collapses to a menu trigger |
| `md` | 48rem (768px) | nav becomes inline, two-column card grid |
| `lg` | 64rem (1024px) | full card grid, larger hero copy |

Responsive behavior is structural (collapse nav, reflow grid), not
fluid typography.

## Components

Every interactive element has: default, hover, focus-visible,
active, disabled. No half-states ship.

- **Top bar** — brand wordmark on the left, primary nav centered
  on `md`+ / hidden behind a menu trigger on small screens, system
  status chip on the right.
- **Status chip** — small pill: dot + label. Three states
  (healthy, degraded, unhealthy) each with a distinct icon + color.
- **Feature card** — `<article>` with: a small status badge in the
  top-right corner, a title, a one-line description, a single
  primary "Open" link styled as a plain text link with an arrow
  (no button chrome — the card itself is the click target).
- **Section heading** — uses the heading; no eyebrow or kicker
  above it. Period weight carries the section identity.
- **State row** — used in the system status panel: label,
  value, status chip.
- **Link** — underlined in the body, never underlined in the nav,
  focus-visible ring uses the accent.

Affordances follow platform conventions: native scrollbars,
native focus rings (overridden only by our `:focus-visible`
treatment), native form controls where they appear.

## Motion

**One authored moment: status change.** When a status chip
transitions (healthy → unhealthy, or vice versa), the dot pulses
once and the icon crossfades. That is the only motion. No
orchestrated page load, no parallax, no hover scale.

- Duration: 180ms on state transitions; 120ms on hover/focus.
- Easing: `cubic-bezier(0.2, 0.7, 0.2, 1)` (a calm
  exponential-out).
- All motion is suppressed when
  `@media (prefers-reduced-motion: reduce)` is set — the
  status change becomes an instant swap.

## Iconography

**Hand-picked line icons in a single weight.** Icons are drawn at
1.5px stroke, 20px default size, square corners, no fills. They
live inline as SVG (no icon font, no third-party package). Each
icon matches its label semantically: a router glyph for Devices,
a magnifier for Discovery, a stack of lines for Logs, a gear for
Settings, and a heartbeat line for the system status.

## States

Every feature card on the home page shows a real status. Until the
back end is wired up, that status is **not_connected** (the chip
reads "Not connected" in muted text with a dashed circle icon).
When the API is wired, the chip becomes **healthy** or
**unhealthy** automatically. The operator never sees a green
checkmark on a feature that has no live data behind it.

Empty states: each feature card placeholder carries a one-line
description that ends with the verb the operator will do
("Discovered cameras and sensors on the LAN", "Live status of
the workers discovery probes", "Recent activity from workers,
messaging-core, and the UI", "Stack configuration, secrets, and
adapter selection").

## Copy

Tone is operator-grade. Verbs come first ("Open", "View",
"Configure"). No exclamation points. No "Welcome to". No "Get
started". No "Let's". The product is named once — in the brand
wordmark — and otherwise referred to as "the stack" or "witsaba".

Error and empty states are specific: "No devices discovered yet"
beats "Nothing to show". "Workers offline" beats "Something went
wrong".

## Accessibility

- All interactive elements have a visible `:focus-visible` ring
  (2px solid accent, 2px offset).
- Status uses icon + label, never color alone. The dot color is
  reinforced by the chip label text.
- Body text ≥ 4.5:1; large text ≥ 3:1; verified for both light
  and dark schemes.
- Tap targets ≥ 40×40px.
- The nav, status panel, and each card are landmark regions
  (`<nav>`, `<section>`, `<article>`) with descriptive `aria-label`s.
- Honors `prefers-reduced-motion` and `prefers-color-scheme`.

## Anti-patterns explicitly refused

- A kicker or eyebrow label above a heading.
- Same-size icon + heading + text cards as the only page
  structure (the home page *is* cards, but the cards are not
  decorative — they carry real status and a single primary
  action, with a system status panel above them).
- Hard offset shadows (`4px 4px 0`).
- Gradient text. Glass. Decorative blur.
- A colored `border-left` stripe on cards.
- Display fonts.
- Sparklines, progress rings, or rounded "soft-shadow"
  stand-ins for data.
- Emoji as icons.
- Orchestrated page-load sequences.
- Modal pop-ups as a first thought.

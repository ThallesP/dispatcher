# DESIGN.md: Railway (railway.com/design)

## Source
- URL: https://railway.com/design/color (+ /type, /button, /forms, /charts, /spacing, /palette)
- Capture date: 2026-07-12
- Evidence: Firecrawl scrape (markdown + branding + html) of the design pages; live browser
  session used to toggle the theme switcher and read the light palette and computed
  button classes (`bg-pink-500 border-pink-500 text-white` on the primary filled button).
- Raw artifacts: `.firecrawl/*.json` (not committed)

## Design Summary
Railway's product design language: dark-first, near-black **purple-tinted** surfaces
(hue ≈ 250), one loud signature **purple** accent (they call the scale "pink", hue 270),
quiet desaturated grays that carry the same violet cast, Inter at a 14px base size,
small radii (6–8px), thin visible borders instead of shadows, and a dotted canvas grid
on dashboard surfaces. Status colors are muted, never neon. Numbers are tabular.

The scales are **mirrored per theme**: `gray-50` is always the step closest to the
background and `gray-950` closest to the foreground, in both light and dark mode. The
`*-500` mid steps are identical in both themes (e.g. `pink-500 = hsl(270 60% 52%)`
everywhere), which is why the brand purple looks the same on white and near-black.

## Design Tokens

### Base surfaces (observed)
| Token | Light | Dark |
| --- | --- | --- |
| background | `hsl(0 0% 100%)` | `hsl(250 24% 9%)` (#13111C — also their `theme-color` meta) |
| secondaryBg | `hsl(0 0% 98%)` | `hsl(250 21% 11%)` |
| foreground | `hsl(250 24% 9%)` | `hsl(0 0% 100%)` |

### Gray scale (theme-adaptive; light-mode values, dark mode reverses the ramp)
`gray-50 hsl(246 4% 97%)` · `100 hsl(246 6% 95%)` · `200 hsl(246 6% 87%)` ·
`300 hsl(246 6% 78%)` · `400 hsl(246 6% 65%)` · `500 hsl(246 6% 55%)` ·
`600 hsl(246 7% 45%)` · `700 hsl(246 8% 35%)` · `800 hsl(246 11% 22%)` ·
`900 hsl(246 18% 15%)` · `950 hsl(248 21% 13%)`

Dark mode uses the same values with the ramp flipped (50 ↔ 950, 100 ↔ 900, …),
except the ends anchor to `hsl(248 21% 13%)` / `hsl(246 4% 97%)`.

### Brand purple ("pink" scale, hue 270 — the Railway accent)
Light-mode ramp: `50 hsl(270 70% 98%)` … `400 hsl(270 70% 65%)` (#A667E4) ·
**`500 hsl(270 60% 52%)` (#853BCE — primary buttons, links, focus)** ·
`600 hsl(270 55% 43%)` … `950 hsl(270 38% 12%)`. Mirrored in dark mode
(`pink-600` dark = `hsl(270 70% 65%)` #A667E4).

### Functional scales (mid steps, both themes)
- blue-500 `hsl(220 80% 55%)` (info)
- cyan-500 `hsl(180 50% 44%)`
- green-500 `hsl(152 38% 42%)` (success — muted, sage-like)
- yellow-500 `hsl(44 74% 52%)` (warning)
- red-500 `hsl(1 62% 44%)` (danger; dark-mode text step `hsl(1 62% 60%)`)

### Typography
- Family: **Inter** (observed in font stack), system-ui fallbacks; monospace for
  code/keybindings. Body base **14px** (`text-sm` default), h1 ≈ 30px bold.
- Heading style: "Huge, bold, punchy." (their words) — tight, high-contrast, sentence case.
- Numeric data: monospace or `tabular-nums`.

### Spacing and layout
- Base unit 4px (Tailwind). Buttons: md ≈ 34px tall, `rounded-md` (6px), `text-sm`,
  `py-1.5 px-3`. Base radius 4–6px, cards ≈ 8–12px. (inferred from computed styles + branding)
- Borders over shadows: 1px lines in gray-100/200; `shadow: none` on buttons.
- Dashboard canvas: subtle dot grid over the background (signature Railway texture).

## Components
- **Buttons**: three variants — Filled (pink-500 bg, white text), Outline (transparent
  bg, gray border), Ghost (transparent, gray-100 hover). Sizes md/sm/xs. Active state
  scales down slightly (`active:scale-95` observed), focus ring 2px.
- **Banners/badges**: Primary (purple), Secondary (gray), Info (blue), Danger (red),
  Success (green), Warning (yellow) — tinted bg (∼100 step) + strong text (∼600 step).
- **Forms**: filled and outline selects, hints in gray-500, errors in red with message
  below; segmented controls for small option sets (their range pickers).
- **Modals**: centered, secondaryBg surface, title + description + footer actions.
- **Charts**: line/area on the functional mid-step colors, purple first.

## Content Style
Concise, technical, a bit playful ("200 Trains", train logo animations). CTAs are short
verbs ("Deploy", "Commit", "Verify"). Sentence case everywhere.

## Agent Build Instructions
1. Map shadcn tokens: `--primary` = pink-500 with white foreground in **both** themes;
   `--background`/`--card` from base surfaces; grays from the hue-246 ramp (never pure gray).
2. Dark mode is first-class: mirror the ramps, don't just darken.
3. Use Inter, 14px base, `tabular-nums` for metrics; bold, tight headings.
4. Radii small (6–8px); 1px borders, no drop shadows.
5. Charts: pink-500 → blue-500 → cyan-500 → green-500 → yellow-500 (600 steps in dark).
6. Add the dot-grid canvas texture behind dashboard content.
7. Don't use Railway's train logo or wordmark — that's their trademark; evoke, don't copy.

## Rerun Inputs
workflow: firecrawl-website-design-clone
source_url: https://railway.com/design/color
target_stack: React Router 8 + Tailwind v4 + shadcn tokens
output: DESIGN.md

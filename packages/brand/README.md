# @veristar/brand

The Veristar brand as code. Every colour, type step, spacing step, radius, shadow, motion value,
layer and layout size used by the console (`apps/console`), the website (`apps/site`), the status page mark
(`assets/`) and the docs (`docs/docs.json`) comes from one file:
[`src/tokens.ts`](src/tokens.ts).

## Files

| File | Written by | What it is |
|---|---|---|
| `src/tokens.ts` | hand | The source: palette, semantic roles per theme, type, spacing, radius, shadow, motion, layers, layout |
| `src/tokens.css` | `generate` | The `--vs-*` custom properties, one block per theme |
| `src/theme.generated.css` | `generate` | The Tailwind 4 `@theme`: clears Tailwind's default palette, type scale, radii and shadows, then maps each utility to a `--vs-*` property |
| `src/spacing.generated.css` | `generate` | Tailwind's spacing step from the spacing scale (`p-4` is 16 px); the console imports it as `@veristar/brand/spacing.css` |
| `src/base.css` | hand | Base styles, keyframes and utilities (`focus-ring`, `field-ring`, `dialog-motion`, `drawer-motion`, `hatch`, `tabular`); only `--vs-*` values |
| `src/index.css` | hand | What an app imports: fonts, tokens, theme, base |
| `src/motion.ts` | hand | Presets for the `motion` library, built from the motion tokens |
| `src/mark.tsx` | hand | The mark as a React component |
| `assets/mark.svg`, `assets/favicon.svg` | `generate` | The mark and favicon the public status page serves |
| `docs/docs.json` (colours and background) | `generate` | Mintlify's accent and ground |
| `docs/favicon.svg`, `docs/logo/*.svg`, each app's `public/favicon.svg` | `generate` | The mark in the docs and both apps |
| each app's `public/theme.js` | `generate` | A copy of `src/theme-boot.js` |

```bash
pnpm --filter @veristar/brand generate   # write every derived file from tokens.ts
pnpm --filter @veristar/brand check      # tsc, derived files up to date, no value outside the tokens
```

`scripts/check-usage.ts` fails on any literal colour, any Tailwind colour or type utility that
names no token, an arbitrary value for a brand property (`text-[13px]`, `bg-[#fff]`,
`rounded-[5px]`), literal durations and motion values (`staggerChildren: 0.05`, `stiffness: 400`),
in `apps/console/src`, `apps/site/src` and this package. In the console it also fails on a
padding, margin or gap outside the spacing scale (`p-[13px]`, `gap-1.5`). Because the default
palette and scale are cleared, `bg-red-500` or `text-sm` do not exist at all.

## Using it in an app

```css
@import "tailwindcss";
@import "@veristar/brand/index.css";
@import "@veristar/brand/spacing.css"; /* the console; the website keeps Tailwind's step */
@source "../../../packages/brand/src";
```

Components name roles, never values: `bg-raised`, `text-fg-2`, `border-line-strong`,
`stroke-series-1`, `text-metric`, `rounded-lg`, `shadow-overlay`,
`duration-(--vs-duration-reveal) ease-standard`, `z-(--vs-layer-toast)`. An SVG attribute takes
the property: `stroke="var(--vs-series-1)"`.

## Specification

**Themes.** Dark (the default) and light. `theme-boot.js` (copied to each app as `/theme.js` and
loaded in `<head>`) sets `data-theme` on `<html>` before the first paint, from the person's saved
choice or the operating system; `src/theme.ts` (`useTheme`) changes it, and the console offers
System, Light and Dark in the account menu. Without `data-theme`, a light operating system gets
the light theme through `prefers-color-scheme`. The values below are the dark theme; the light
theme in `tokens.ts` maps the same roles onto a warm paper ground (#F6F5F2) with white cards.

**Surfaces**, deepest to highest: `sunken` #121211 (inputs, code), `ground` #151514 (page),
`raised` #1C1C1A (cards, tables), `overlay` #22221F (menus, tooltips, dialogs), with `hover`
#252522, `selected` #2A2A27, `muted` #33322E (skeletons, at least 1.3:1 on a card) and a 60% `scrim` behind dialogs.
Raised surfaces have a border and no shadow; overlays add `shadow-overlay`, dialogs `shadow-dialog`. A shadow's colour is the theme's `shadow-soft` or `shadow-strong` role.

**Lines.** `line` #2E2D29 by default, `line-strong` #3D3C37 for controls and the chart
baseline, `line-subtle` #252522 for row rules and the chart grid.

**Text.** `fg` #EEEDE8, `fg-2` #A6A49C, `fg-3` #97958C, `fg-disabled` #737169. Every text pair
with the surfaces up to `selected` passes WCAG 2.2 AA (the ratios are noted beside the roles in
`tokens.ts`).

**Disabled.** A control that cannot be used has a flat `disabled-fill` #252522, `disabled-fg`
#737169 text (3.1:1 on the fill) and a `disabled-line` #2E2D29 edge, in every variant. A disabled primary is never a
dim accent and a disabled outline never looks enabled.

**Pressed.** A toggle that is on (a chart option, a table view) has the `pressed` #2A2A27 surface
and a `pressed-line` #6E6C65 edge.

**Accent.** Terracotta: `accent` #B5482E with white `on-accent` (5.3:1), `accent-hover` #A13F28,
`accent-pressed` #8A3622. It fills the single primary action per screen and is never a status.
`accent-text` (#F29A80 on dark, #A13F28 on light) is the accent drawn on a surface: links, tab
markers, the focus ring and the star in the mark.

**Status**, always with an icon and a word: `success` #3FB27F (verified, received, active),
`warning` #E0A54A (needs action, 4xx; #A35A00 on light), `critical` #F26A90 (refused, failing,
not approved, 5xx, paging alert; #940543 on light), `info` = `fg-2` (neutral: waiting, removed).
Each has a `-soft` fill and a `-line` border. Critical is a cool crimson, kept apart from the
terracotta accent (OKLab distance x100 of the fills: 11.0 normal, 12.3 protan, 9.1 deutan). A
destructive action in a page, row or menu is crimson text with an icon; only the confirm button
of a destructive dialog is filled, with `critical-solid` #A81650 (`critical-solid-hover`
#940543) under white `on-critical`.

**Data series**, in this fixed order: #3987E5 blue, #D55181 magenta, #9085E9 violet, #199E70
aqua, #C98500 ochre. Validated on the raised surface: worst adjacent CVD ΔE 8.4, normal vision
19.7, each at least 3:1. The light theme steps the same hues for a white card (#2A6FD6, #C23F78,
#6E5ED6, #14876A, #A66B00: worst adjacent CVD ΔE 9.5, normal vision 17.5). One series is always
blue. The previous period is `series-prev`
#6E6C65, dashed. A period that has not ended (a partial day or hour) keeps its series colour with
`series-partial` stripes cut into it (#1C1C1A, the card surface) and has a "Partial" legend
entry. Status colours are never series colours.

**Type.** Inter (variable, with its optical size axis) for text, JetBrains Mono for numbers, keys and code only. Both SIL OFL, self-hosted from fontsource.

| Step | Size / line | Weight | Use |
|---|---|---|---|
| `axis` | 11 / 14 | 400 | chart ticks |
| `caption` | 12 / 16 | 500 | labels, table headers |
| `small` | 13 / 20 | 400 | secondary text, compact rows |
| `body` | 14 / 20 | 400 | default |
| `title` | 16 / 24 | 600 | card and dialog titles |
| `section` | 20 / 28 | 600 | sections |
| `heading` | 22 / 28 | 600 | console page titles |
| `page` | 28 / 36 | 600 | website page titles |
| `metric` | 28 / 36 | 500 | the value of a stat card (mono) |
| `h1` | 32 / 40 | 600 | website section titles |
| `display` | 48 / 52 | 600 | the website hero |

Tracking: `caption` 0.01em, `section` -0.01em, `heading` -0.015em, `metric` -0.02em, `page`
-0.025em, `h1` -0.03em, `display` -0.035em, the rest 0.

**Space.** 4 px base, as tokens: 0, 1 = 4, 2 = 8, 3 = 12, 4 = 16, 5 = 20, 6 = 24, 8 = 32,
10 = 40, 12 = 48, 16 = 64. In the console the step key is the Tailwind key (`p-5` is 20 px) and
no other padding, margin or gap exists. Rhythm: page gutter 24 (16 on a phone), gap between page
sections 32, card padding 20 (16 on a phone), gap inside a card 12, form field gap 16. Compact
rows 36 px, comfortable 48 px; controls 36 px, small 32 px, 44 px touch targets on a phone. The
website keeps Tailwind's own step for now (a quarter of its 14 px root).

**Radius.** 2 (bar ends), 4 (chips), 6 (controls), 10 (cards), 12 (panels, dialogs), 16 (the
website hero), full.

**Icons.** lucide only, 16 px at stroke 2, 14 px in compact rows. No filled icons, no emoji.

**Motion.** One easing, `cubic-bezier(0.2, 0, 0, 1)` (`ease-standard`), and an exit easing.
Each motion means something: *press* 120 ms (a click registered), *reveal* 180 ms (menus,
tooltips, popovers rise 4 px from their trigger), *change* 240 ms (dialogs, values counting to a
new number, never on page load), *arrival* 240 ms plus a 1.2 s highlight (a new row or event),
*page* 200 ms (a page or tab settles in), *number* 400 ms (a value counting to its new number),
*live* 2 s (the breathing dot of anything listening). Lists reveal their children 30 ms apart
after a 20 ms wait (`motion.stagger`); layout moves use one spring (`motion.spring`, stiffness
520, damping 42, mass 0.8). With reduced motion only opacity changes remain.

**Layers.** `raised` 10, `header` 30, `overlay` 50 (menus, tooltips), `toast` 60. A native
`<dialog>` sits in the browser's top layer above all of them.

**Mark.** An orbit circle crossed by a meridian with the accent star at its top
(`src/mark.tsx`, `docs/logo/veristar.svg`, the favicons).

## Changing the brand

Edit `src/tokens.ts`, run `generate`, commit the derived files with it. Nothing else changes:
components only name roles.

## Adding a theme

1. Add it to `themes` in `src/tokens.ts` with the same role keys (TypeScript fails on a missing
   role) and its `colorScheme`.
2. Run `generate`. `tokens.css` gains a `[data-theme="<name>"]` block.
3. Offer it in `src/theme.ts` and `src/theme-boot.js`.

Components need no change. Check the new roles with the dataviz palette validator for the series
and the text pairs against WCAG 2.2 AA (4.5:1 for text, 3:1 for control edges and marks), and
note the ratios beside the roles as the existing themes do.

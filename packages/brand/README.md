# @veristar/brand

The Veristar brand as code. Every colour, type step, radius, shadow, motion value and layout
size used by the console (`apps/console`), the website (`apps/site`), the status page mark
(`assets/`) and the docs (`docs/docs.json`) comes from one file:
[`src/tokens.ts`](src/tokens.ts).

## Files

| File | Written by | What it is |
|---|---|---|
| `src/tokens.ts` | hand | The source: palette, semantic roles per theme, type, radius, shadow, motion, layout |
| `src/tokens.css` | `generate` | The `--vs-*` custom properties, one block per theme |
| `src/theme.generated.css` | `generate` | The Tailwind 4 `@theme`: clears Tailwind's default palette, type scale, radii and shadows, then maps each utility to a `--vs-*` property |
| `src/base.css` | hand | Base styles, keyframes and utilities (`focus-ring`, `field-ring`, `dialog-motion`, `drawer-motion`, `hatch`, `tabular`); only `--vs-*` values |
| `src/index.css` | hand | What an app imports: fonts, tokens, theme, base |
| `src/motion.ts` | hand | Presets for the `motion` library, built from the motion tokens |
| `src/mark.tsx` | hand | The mark as a React component |
| `assets/mark.svg`, `assets/favicon.svg` | `generate` | The mark and favicon the public status page serves |
| `docs/docs.json` (colours and background) | `generate` | Mintlify's accent and ground |

```bash
pnpm --filter @veristar/brand generate   # write every derived file from tokens.ts
pnpm --filter @veristar/brand check      # tsc, derived files up to date, no value outside the tokens
```

`scripts/check-usage.ts` fails on any literal colour, any Tailwind colour or type utility that
names no token, an arbitrary value for a brand property (`text-[13px]`, `bg-[#fff]`,
`rounded-[5px]`) and literal durations, in `apps/console/src`, `apps/site/src` and this package.
Because the default palette and scale are cleared, `bg-red-500` or `text-sm` do not exist at all.

## Using it in an app

```css
@import "tailwindcss";
@import "@veristar/brand/index.css";
@source "../../../packages/brand/src";
```

Components name roles, never values: `bg-raised`, `text-fg-2`, `border-line-strong`,
`stroke-series-1`, `text-metric`, `rounded-lg`, `shadow-overlay`,
`duration-(--vs-duration-reveal) ease-standard`. An SVG attribute takes the property:
`stroke="var(--vs-series-1)"`.

## Specification

**Themes.** Dark (the default) and light. `theme-boot.js` (copied to each app as `/theme.js` and
loaded in `<head>`) sets `data-theme` on `<html>` before the first paint, from the person's saved
choice or the operating system; `src/theme.ts` (`useTheme`) changes it, and the console offers
System, Light and Dark in the account menu. Without `data-theme`, a light operating system gets
the light theme through `prefers-color-scheme`. The values below are the dark theme; the light
theme in `tokens.ts` maps the same roles onto a warm paper ground (#F6F5F2) with white cards.

**Surfaces**, deepest to highest: `sunken` #121211 (inputs, code), `ground` #151514 (page),
`raised` #1C1C1A (cards, tables), `overlay` #22221F (menus, tooltips, dialogs), with `hover`
#252522, `selected` #2A2A27, `muted` #232320 (skeletons) and a 60% `scrim` behind dialogs.
Raised surfaces have a border and no shadow; overlays add `shadow-overlay`, dialogs `shadow-dialog`. A shadow's colour is the theme's `shadow-soft` or `shadow-strong` role.

**Lines.** `line` #2E2D29 by default, `line-strong` #3D3C37 for controls and the chart
baseline, `line-subtle` #252522 for row rules and the chart grid.

**Text.** `fg` #EEEDE8, `fg-2` #A6A49C, `fg-3` #918F86, `fg-disabled` #6E6C65. Every pair with
the surfaces passes WCAG 2.2 AA (checked by the Umami theme's asset script).

**Accent.** Terracotta: `accent` #B5482E with white `on-accent` (5.3:1), `accent-hover` #A13F28,
`accent-pressed` #8A3622. It fills the single primary action per screen and is never a status.
`accent-text` (#F29A80 on dark, #A13F28 on light) is the accent drawn on a surface: links, tab
markers, the focus ring and the star in the mark.

**Status**, always with an icon and a word: `success` #3FB27F (verified, received, active),
`warning` #E0A54A (pending, quarantined, 4xx), `critical` #F97066 (failed, rejected, 5xx,
paging alert), `info` = `fg-2` (neutral, suspended). Each has a `-soft` fill and a `-line`
border. Destructive buttons use `critical-solid` #C93A36 with white `on-critical`.

**Data series**, in this fixed order: #3987E5 blue, #D55181 magenta, #9085E9 violet, #199E70
aqua, #C98500 ochre. Validated on the raised surface: worst adjacent CVD ΔE 8.4, normal vision
19.7, each at least 3:1. The light theme steps the same hues for a white card (#2A6FD6, #C23F78,
#6E5ED6, #14876A, #A66B00: worst adjacent CVD ΔE 9.5, normal vision 17.5). One series is always
blue. The previous period is `series-prev`
#6E6C65, dashed. Status colours are never series colours.

**Type.** Inter (variable, with its optical size axis) for text, JetBrains Mono for numbers, keys and code only. Both SIL OFL, self-hosted from fontsource.

| Step | Size / line | Weight | Use |
|---|---|---|---|
| `axis` | 11 / 14 | 400 | chart ticks |
| `caption` | 12 / 16 | 500 | labels, table headers |
| `small` | 13 / 20 | 400 | secondary text, compact rows |
| `body` | 14 / 20 | 400 | default |
| `title` | 16 / 24 | 600 | card and dialog titles |
| `section` | 20 / 28 | 600 | sections |
| `page` | 24 / 32 | 600 | page titles |
| `metric` | 28 / 36 | 500 | the value of a stat tile (mono) |
| `h1` | 32 / 40 | 600 | website section titles |
| `display` | 48 / 52 | 600 | the website hero |

**Space.** 4 px base: 4, 8, 12, 16, 20, 24, 32, 40, 48, 64. Compact rows 36 px, comfortable 48
px; controls 36 px, small 32 px, 44 px touch targets on a phone.

**Radius.** 2 (bar ends), 4 (chips), 6 (controls), 10 (cards), 12 (panels, dialogs), 16 (the
website hero), full.

**Icons.** lucide only, 16 px at stroke 2, 14 px in compact rows. No filled icons, no emoji.

**Motion.** One easing, `cubic-bezier(0.2, 0, 0, 1)` (`ease-standard`), and an exit easing.
Each motion means something: *press* 120 ms (a click registered), *reveal* 180 ms (menus,
tooltips, popovers rise 4 px from their trigger), *change* 240 ms (dialogs, values counting to a
new number, never on page load), *arrival* 240 ms plus a 1.2 s highlight (a new row or event),
*page* 200 ms (a page or tab settles in), *live* 2 s (the breathing dot of anything listening).
With reduced motion only opacity changes remain.

**Mark.** An orbit circle crossed by a meridian with the accent star at its top
(`src/mark.tsx`, `docs/logo/veristar.svg`, the Umami favicon).

## Changing the brand

Edit `src/tokens.ts`, run `generate`, commit the derived files with it. Nothing else changes:
components only name roles.

## Adding a theme

1. Add it to `themes` in `src/tokens.ts` with the same role keys (TypeScript fails on a missing
   role) and its `colorScheme`.
2. Run `generate`. `tokens.css` gains a `[data-theme="<name>"]` block.
3. Offer it in `src/theme.ts` and `src/theme-boot.js`.

Components need no change. Check the new roles with the dataviz palette validator for the series
and with the Umami theme's contrast check for text pairs.

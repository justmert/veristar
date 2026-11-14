---
title: "0004: Veristar theme on Umami's dashboard"
sidebarTitle: "0004: Umami dashboard theme"
description: "How web.<BASE_DOMAIN> shows Umami's dashboard in Veristar's colours, fonts and icons while the Umami image stays official and unmodified."
---

- Status: accepted
- Date: 2026-11-14

## Context

Tenants read their web analytics in Umami's own dashboard on `web.<BASE_DOMAIN>`, which they
reach from the console through SSO. The console and the public site carry the
Veristar brand; the dashboard looked like stock Umami. The question was whether the dashboard
can take the Veristar brand without breaking the RFP or the platform's own guarantees:

- RFP section 2: the platform is "built upon either Umami or GoatCounter".
- RFP section 3.4: infrastructure "can be rebuilt from the open-source monorepo", and a
  community operator must be able to run the whole system after handover.
- ADR 0002: the official Umami image runs unmodified, pinned by digest, and an upgrade is a
  digest change plus `make umami-upgrade`, not a merge.
- `deploy/caddy/web.Caddyfile`: a deny list and method allowlist in front of Umami, a
  log filter that drops every client identifier, and no response header that weakens Umami's
  own Content-Security-Policy.
- Privacy strategy: no third-party requests from any page the platform serves; Umami runs
  with `PRIVATE_MODE=1`, `DISABLE_TELEMETRY=1` and `DISABLE_UPDATES=1`.

## Findings

Everything below was read at the pinned tag
[`v3.5.0`](https://github.com/umami-software/umami/tree/v3.5.0) (commit `d3c62a8`), in
`@umami/react-zen` 0.255.0 (the version `pnpm-lock.yaml` of that tag resolves), and against the
pinned image `ghcr.io/umami-software/umami:3.5.0@sha256:b64f4040...a5dbc` running in the
development stack.

**Umami has no branding option.**

- The [environment variable reference](https://docs.umami.is/docs/environment-variables) lists
  no variable for colours, logo, favicon, title or stylesheet. The only theme setting is the
  per-browser light or dark choice under
  [Preferences](https://docs.umami.is/docs/preferences), saved in the browser.
- `next.config.ts` passes only API, base path, cloud, currency, locale and self-tracking values
  into the client
  ([next.config.ts lines 14 to 26](https://github.com/umami-software/umami/blob/v3.5.0/next.config.ts#L14-L26)).
- The root layout hard-codes the icon links, the theme colours of the browser chrome and the
  title template `%s | Umami`
  ([src/app/layout.tsx lines 36 to 47 and 67](https://github.com/umami-software/umami/blob/v3.5.0/src/app/layout.tsx#L36-L67)).
- The only "white label" in the code is for share pages: it is read from a Redis key that
  Umami Cloud fills, and it is skipped when Redis is not configured
  ([src/app/api/share/\[slug\]/route.ts lines 37 to 49](https://github.com/umami-software/umami/blob/v3.5.0/src/app/api/share/%5Bslug%5D/route.ts#L37-L49),
  [ShareBranding.tsx lines 11 to 13](https://github.com/umami-software/umami/blob/v3.5.0/src/app/share/%5Bslug%5D/%5B%5B...path%5D%5D/ShareBranding.tsx#L11-L13)).
  The platform denies share routes and leaves `REDIS_URL` unset, so it does not apply.
- The maintainer declined a branding pull request in 2022: "we are not going to include any
  branding efforts in Umami"
  ([PR 1621](https://github.com/umami-software/umami/pull/1621#issuecomment-1319102430)).

**The theme is a set of CSS custom properties.**

- Umami's UI is built with `@umami/react-zen`
  ([package.json line 90](https://github.com/umami-software/umami/blob/v3.5.0/package.json#L90))
  and imports its full stylesheet in the root layout
  ([layout.tsx line 11](https://github.com/umami-software/umami/blob/v3.5.0/src/app/layout.tsx#L11)).
- Zen's components take every colour, the font, the radius and the shadow from `--zen-*`
  custom properties. `styles.full.css` of the package defines them on `:root`, on
  `[data-theme=light]` and on `[data-theme=dark],.dark`, and a neutral ramp
  `--zen-palette-50` to `-950` on `:root` and `[data-palette=*]`.
- Umami's own `global.css` sets `--zen-font-family` to its Inter font and overrides
  `--zen-primary` with its blue in every mode
  ([src/app/global.css lines 1 to 12](https://github.com/umami-software/umami/blob/v3.5.0/src/app/global.css#L1-L12)).
- The mode is chosen in the browser by zen's `useTheme`: the stored value under the
  `localStorage` key `theme`, else `prefers-color-scheme`. Zen then writes `data-theme` and the
  `dark` class on `<html>`
  ([react-zen 0.255.0 dist/index.mjs lines 7455 to 7531](https://unpkg.com/@umami/react-zen@0.255.0/dist/index.mjs)).
  `ZenProvider` is mounted without a theme
  ([src/app/Providers.tsx line 49](https://github.com/umami-software/umami/blob/v3.5.0/src/app/Providers.tsx#L49)),
  so the first visit of a browser whose system is set to light shows light mode.

**Charts are not CSS.** Bar and line charts are Chart.js canvases
([src/components/charts/Chart.tsx line 174](https://github.com/umami-software/umami/blob/v3.5.0/src/components/charts/Chart.tsx#L174)).
Their colours are JavaScript strings, not CSS variables: `THEME_COLORS` holds one blue
(`#2680eb`) for both modes and grey grid and tick colours per mode, and `CHART_COLORS` the
categorical palette
([src/lib/constants.ts lines 242 to 270](https://github.com/umami-software/umami/blob/v3.5.0/src/lib/constants.ts#L242-L270)).
`getThemeColors` turns that blue into the dataset colours with `colord`
([src/lib/colors.ts lines 60 to 84](https://github.com/umami-software/umami/blob/v3.5.0/src/lib/colors.ts#L60-L84)),
and the charts spread them into each Chart.js dataset, for example the visitors and views series
([src/components/metrics/PageviewsChart.tsx lines 25 and 31 to 50](https://github.com/umami-software/umami/blob/v3.5.0/src/components/metrics/PageviewsChart.tsx#L25-L50)).
Chart.js paints them onto the canvas, where no stylesheet reaches.
The world map is SVG with its fill written as a presentation attribute
([src/components/metrics/WorldMap.tsx lines 41 to 93](https://github.com/umami-software/umami/blob/v3.5.0/src/components/metrics/WorldMap.tsx#L41-L93)),
which a stylesheet can override.

**Umami's CSP already allows a same-origin stylesheet.** Umami sets
`default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-eval' 'unsafe-inline'`
([src/lib/csp.ts lines 12 to 28](https://github.com/umami-software/umami/blob/v3.5.0/src/lib/csp.ts#L12-L28)),
per request in the Docker image
([docker/proxy.ts line 102](https://github.com/umami-software/umami/blob/v3.5.0/docker/proxy.ts#L102)).
`web.Caddyfile` adds no CSP of its own. A `<link rel="stylesheet">` to a path on the same
host, fonts from the same host and images from the same host are all allowed without any
change to that policy.

**The web Caddy already rewrites Umami's HTML.** Since this stage it adds the DB-IP attribution
before `</body>` with the `replace-response` plugin, on GET answers with an HTML content type
only, and the live tests prove the node survives React's hydration and client-side navigation
(`tests/caddy-web/specs/attribution.spec.ts`). React 19 skips unknown tags in `<head>` and
`<body>` when it hydrates.

**The sidebar mark and name.** The navigation header renders Umami's logo, an inline SVG with
the viewBox `0 0 428 389.11` that no other icon uses
([src/components/svg/Logo.tsx](https://github.com/umami-software/umami/blob/v3.5.0/src/components/svg/Logo.tsx)),
inside an icon span followed by a text span holding "umami"
([src/app/(main)/SideNav.tsx lines 100 to 104](https://github.com/umami-software/umami/blob/v3.5.0/src/app/(main)/SideNav.tsx#L100-L104),
the same in `MobileNav.tsx`;
[src/components/common/IconLabel.tsx](https://github.com/umami-software/umami/blob/v3.5.0/src/components/common/IconLabel.tsx)).

**The delta chips.** The up and down percentages under each metric take their ground from
zen's status variables, `--zen-status-success-bg` and `--zen-status-error-bg`, through an
inline style
([src/components/metrics/ChangeLabel.tsx lines 5 to 18](https://github.com/umami-software/umami/blob/v3.5.0/src/components/metrics/ChangeLabel.tsx#L5-L18)).
They are therefore CSS-reachable.

**Static assets.** The HTML links `/favicon.ico`, `/apple-touch-icon.png`,
`/favicon-32x32.png`, `/favicon-16x16.png`, `/site.webmanifest` and `/safari-pinned-tab.svg`;
the manifest names `/android-chrome-192x192.png` and `/android-chrome-512x512.png`, and
`/browserconfig.xml` names `/mstile-150x150.png`
([public/](https://github.com/umami-software/umami/tree/v3.5.0/public)). Umami's Inter font is
built into `/_next/static/media/` by `next/font` and served from the same host.

## Options

### (a) Umami's own options only

Not enough. The only lever is each user's light or dark preference. Colours, font, icons and
title cannot be set by configuration. Safe, but it does not brand anything.

### (b) Overrides served by the web Caddy

One stylesheet, linked into Umami's HTML before `</head>` by the existing rewrite, redefines
zen's custom properties. The icons and the manifest are answered by Caddy at Umami's own paths.
Fonts are self-hosted next to the stylesheet. No script, no image change.

- **Security.** No JavaScript is added, so the dashboard origin, which holds a tenant's Umami
  token, runs exactly Umami's code. The new paths are static files: Caddy answers them with
  `file_server`, which serves GET and HEAD and refuses other methods with 405, and a missing
  file is a 404 from Caddy, never a request to Umami. The deny list, the method allowlist, the
  SSO rules and the log filter are unchanged and stay covered by `make test-caddy-web`.
- **CSP.** Unchanged. The stylesheet, fonts and icons are same-origin, which Umami's policy
  allows. The theme files carry `default-src 'none'` and `nosniff`, so an SVG opened on its own
  runs nothing.
- **Privacy.** Every new request goes to `web.<BASE_DOMAIN>` itself. No CDN, no third-party
  font. `TestWebPath`'s egress check is unaffected.
- **Upgrade fragility.** The theme depends on names, not on code: 46 `--zen-*`
  properties, three Tailwind radius properties, the classes `bg-primary`, `bg-status-error`
  and `text-white`, the `data-theme` attribute, two map fill constants, the logo's viewBox and
  the structure around it, the chips' status variables, the `| Umami` title, `</head>` and
  the icon paths. `tests/umami-compat/theme_test.go` checks all of them against the running image and
  checks that the theme was reviewed for that exact Umami version, so `make umami-upgrade`
  stops on a rename instead of shipping a half-themed dashboard. If a name does slip through,
  the failure mode is cosmetic: that part of the page falls back to Umami's own style.
- **Handover.** The theme is one CSS file with a single block of brand values, a font and icon
  generator (`pnpm --filter @veristar/umami-theme assets`) and a check that runs in
  `pnpm check`. It ships inside the console image, so a rebuild from the monorepo reproduces
  it. Removing it is two edits to `web.Caddyfile`
  ([operators guide](../operators/umami-dashboard-theme.md)).
- **RFP fit.** Umami stays the official image. The sidebar shows the Veristar mark and name,
  and Umami stays credited in the title of every page (`Overview | Umami`), which the theme
  does not touch: the dashboard still says what it is built upon. Umami shows no footer or
  "powered by" line inside the app that could carry the credit instead; its preferences page
  shows only the bare version number. `theme_test.go` fails if the title loses the credit.
- **Sidebar.** The stylesheet hides Umami's logo SVG and draws `brand/mark.svg` as the icon
  span's background, and hides the word "umami" (no size, not visible, so screen readers skip
  it) while the span's `::after` shows and reads "Veristar" in Inter 600 with -0.03em tracking.
  The selectors are the viewBox and the "icon span, then text span" structure; `theme_test.go`
  checks the viewBox in Umami's JavaScript, and `tests/umami-compat/specs/ui.spec.ts` checks the
  rendered structure and the word "umami" in a real browser.
- **Delta chips.** `--zen-status-*-bg` are set to the neutral muted surface (`--vs-muted`), so
  the chips show their status by text and arrow colour only, never by a tinted fill.
  `theme.spec.ts` asserts the computed ground and colours.

Limits, all visible in the screenshots:

- Chart series keep Umami's blue and its categorical palette, an accepted known limit: they
  are JavaScript constants painted on canvas (see Findings), so the brand mapping of the primary
  series to ink `#EEEDE8` and comparisons to `#8E8C84` and `#6E6C65` cannot be applied without
  patching Umami. A CSS filter on the canvas could shift the hue but not map colours exactly,
  and would distort every series at once, so it is not used for that.
- Light mode draws charts with Umami's light grid colours. The stylesheet inverts light-mode
  canvases and turns their hue back, which gives dark grid lines on the dark card; series
  colours are then close to, not exactly, the dark-mode ones.
- The page title keeps the `| Umami` suffix, on purpose as the credit, and because Next.js
  sets the title again on every client-side navigation from the RSC payload, which a rewrite of
  the HTML does not reach.
- The `theme-color` and tile colour meta tags keep Umami's values (they colour the browser
  chrome on some phones). Changing them would mean rewriting attributes that React hydrates.

### (c) A fork or a custom image

Rejected. It would give full control of colours, charts and titles, but every Umami release
(3.5.0 alone carried a list of security fixes) would become a rebase and an image build of our
own before it could ship, which works against the 24-hour patch target of RFP 3.4 and against
ADR 0002. A future operator would have to maintain the fork as well. The image's signatures
and SBOM would be ours instead of upstream's.

### (d) A web dashboard of our own in the console

Only as a comparison. The console could draw web analytics from Umami's read API (as it
already does for operator views), fully on-brand. It would mean rebuilding Umami's overview,
realtime, sessions, events, compare, breakdown and filters, and keeping them in step with each
Umami release. That is a product of its own, it duplicates what "built upon Umami" provides,
and it is not needed to answer the question asked here.

## Decision

Option (b). The web Caddy serves `deploy/caddy/umami-theme/site` at `/srv/umami-theme` and
links `/_veristar/theme.css` before `</head>` of Umami's HTML. The console image carries the
directory, next to the console build, so a release ships both.

The dashboard theme is dark only (the console and website also have a light theme). Umami keeps
its light and dark switch, and there is no way to force dark without JavaScript, so both modes
render the same dark palette: the semantic tokens are
identical, and in light mode the neutral ramp is reversed so the rules that use a light step
directly still land on dark values.

## Consequences

- Every token pair of the brand is held to WCAG 2.2 AA by `pnpm check`, and the rendered
  dashboard is checked with axe's colour-contrast rule in both modes by `make test-caddy-web`.
- The brand values live in one block at the top of `theme.css`. A brand change is an edit
  there, then `pnpm --filter @veristar/umami-theme assets` if the icon changed.
- An Umami upgrade now also reviews the theme. The upgrade runbook says what to look at.

## Sources

- Umami v3.5.0 source and documentation, linked above.
- `@umami/react-zen` 0.255.0 from the npm registry: `styles.full.css` and `dist/index.mjs`.
- ADR 0002, `deploy/caddy/web.Caddyfile`, `docs/compliance/privacy-strategy.md`.

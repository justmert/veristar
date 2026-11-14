---
title: "Umami dashboard theme"
description: "How tenants' Umami dashboard on web.<BASE_DOMAIN> gets the Veristar colours, fonts and icons, and how to change or remove them."
---

Tenants read their web analytics in Umami's own dashboard on `web.<BASE_DOMAIN>`. Umami runs as
the official, unmodified image. The Veristar look comes from the web Caddy in front of it
([ADR 0004](../adr/0004-umami-dashboard-theme.md)):

- Caddy links one stylesheet, `/_veristar/theme.css`, just before `</head>` of every HTML page
  Umami sends. The stylesheet redefines the CSS custom properties of Umami's design system
  (`--zen-*`), so colours, font, radius and shadows follow the brand. It adds no script.
- Caddy answers Umami's icon paths (`/favicon.ico`, `/apple-touch-icon.png`, the PNG sizes,
  `/site.webmanifest`, `/browserconfig.xml`, `/safari-pinned-tab.svg`) from the theme, so tabs
  and home-screen shortcuts show the Veristar mark.
- Fonts (Geist and Geist Mono) are served from the same host. Nothing is fetched from another
  host.

The brand is dark only. Umami still offers its light and dark switch; both render the same dark
palette.

The sidebar shows the Veristar mark and the name "Veristar" in place of Umami's (a stylesheet
swap, no script), and the up and down chips under each metric show their status by text colour
on the neutral surface.

What stays Umami's: chart series colours (Umami's blue and its categorical palette, painted on
canvas from Umami's JavaScript), the `| Umami` suffix of every page title, which is where the
dashboard credits Umami, and the browser chrome colour on some phones. ADR 0004 explains why
each one stays.

## Where the files are

| Path | What |
|---|---|
| `deploy/caddy/umami-theme/site/_veristar/theme.css` | the stylesheet; brand values in the `--vs-*` block at its top |
| `deploy/caddy/umami-theme/site/_veristar/brand/` | `mark.svg`, `favicon.svg` (the source of every raster icon) and `mark-mono.svg` |
| `deploy/caddy/umami-theme/site/_veristar/fonts/` | generated: the fonts, their `@font-face` file and their licences |
| `deploy/caddy/umami-theme/site/*.png`, `favicon.ico`, `safari-pinned-tab.svg` | generated: the icons |
| `deploy/caddy/umami-theme/site/site.webmanifest`, `browserconfig.xml` | written by hand |
| `deploy/caddy/umami-theme/assets.ts` | generates the files marked generated, and checks them |
| `deploy/caddy/web.Caddyfile` | the `@theme` route and the `</head>` rewrite |

The console image (`deploy/images/console.Dockerfile`) copies `site/` to `/srv/umami-theme`, the
directory `web.Caddyfile` serves, so the theme ships with every platform release and needs no
setting in the inventory.

## Change the theme

1. **Colours, font, radius or shadow:** edit only the `--vs-*` block at the top of `theme.css`.
   The rules below it map those values onto Umami's properties.
2. **The mark:** edit `site/_veristar/brand/favicon.svg` (and `mark.svg`, `mark-mono.svg`), then
   regenerate the icons:

   ```sh
   pnpm --filter @veristar/umami-theme assets
   ```

   It renders the PNG and ICO files in Chromium (Playwright's browser must be installed) and
   copies the fonts from the packages the pnpm catalog pins.
3. **Check:**

   ```sh
   pnpm check                # includes the theme check below
   pnpm --filter @veristar/umami-theme check
   make test-caddy-web
   ```

   The theme check fails when a generated file is out of date and when a brand colour pair falls
   under WCAG 2.2 AA (4.5:1 for text, 3:1 for the focus ring). `make test-caddy-web` serves the
   theme through the real web Caddy in front of a real Umami, checks every file, the CSP and the
   deny list, runs axe's colour-contrast rule on the dashboard with the system in dark and in
   light mode, and saves screenshots of both to `.scratch/design/umami/`.
4. **Ship** with a normal release (`release-images.yml`, then the rolling deploy). Browsers
   revalidate the theme files on every load (`Cache-Control: no-cache`), so the new theme shows
   at the next page load.

## Remove the theme

Umami's own look comes back, and nothing else changes.

1. In `deploy/caddy/web.Caddyfile` delete the `@theme` matcher with its `route @theme { ... }`
   block, and the `</head>` line inside `replace @page { ... }`. Keep the `</body>` line: it is
   the DB-IP attribution, which the licence of the geolocation database requires.
2. Run `make test-caddy-web`, release and deploy. With only the Caddyfile changed, the core role
   reloads Caddy in place.
3. Optionally delete `deploy/caddy/umami-theme/`, its entry in `pnpm-workspace.yaml`, the `COPY`
   line in `deploy/images/console.Dockerfile`, the theme bind in the test harnesses
   (`tests/fixtures/umamictr`, `tests/e2e-console`, `tests/handover`,
   `tests/observability/compose.yml`), `tests/umami-compat/theme_test.go`,
   `tests/caddy-web/specs/theme.spec.ts` and the `webTheme` subtest of
   `deploy/caddy/web_live_test.go`.

## After an Umami upgrade

`make umami-upgrade` stops at `umami-compat` until the theme has been reviewed for the new
version. See [the upgrade runbook](../runbooks/umami-upgrade.md).

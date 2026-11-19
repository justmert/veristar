# Veristar website

The public website at `<BASE_DOMAIN>` and `www.<BASE_DOMAIN>`: a static React page that says what
Veristar is, who it is for and how it treats data. It holds no data and calls no API.

## Build

From the repository root:

```sh
pnpm install
pnpm --filter @veristar/site build    # writes apps/site/dist
pnpm --filter @veristar/site check    # typecheck
node packages/brand/scripts/check-usage.ts
```

Local preview: `pnpm --filter @veristar/site dev` (port 5173), or serve the build with
`cd apps/site && npx vite preview --port 5190 --strictPort`.

## Links

The page reads its own hostname. On `www.<domain>` or `<domain>` it links to `app.<domain>/sign-in`
(the console), `docs.<domain>` and `status.<domain>`. On any other host (localhost, a preview
host) the links point at the `veristar.app` hosts, so the page keeps its layout. Sign-in follows
`VITE_CONSOLE_ORIGIN` when the build sets it.

GitHub links appear only when the build sets `VITE_REPO_URL`. Leave it unset while the repository
is private:

```sh
VITE_REPO_URL=https://github.com/<owner>/<repo> pnpm --filter @veristar/site build
```

## Metadata

The canonical link, the Open Graph tags, `robots.txt` and `sitemap.xml` use `VITE_SITE_ORIGIN`
(default `https://veristar.app`). A build for another domain sets it:

```sh
VITE_SITE_ORIGIN=https://example.org pnpm --filter @veristar/site build
```

`public/og.png` (1200 x 630) is a capture of the top of the page.

## Tracker

The site measures itself with Veristar, as the site `veristar.app`. The tag in `index.html` is the
one from the console, in the same form as
[the static HTML guide](../../docs/onboarding/web/static-html.md): a pinned tracker version with an
`integrity` hash, loaded from the gateway `https://in.veristar.app`. The policy below allows that
origin in `script-src` (the file) and `connect-src` (the challenge and the events), as
[the content security policy guide](../../docs/onboarding/web/content-security-policy.md) asks.
A new tracker version is a new tag from the console.

## Serving

`dist/` is a plain static directory: `index.html`, `404.html`, `favicon.svg`,
`apple-touch-icon.png`, `og.png`, `robots.txt`, `sitemap.xml`, `theme.js` and hashed files in
`assets/`. Any static file server works. It belongs on the core tier beside the console, served
by a Caddy site for `<BASE_DOMAIN>` and `www.<BASE_DOMAIN>`, with:

- `404.html` answered with status 404 for every path that is not a file;
- `Cache-Control: public, max-age=31536000, immutable` on `assets/*`, and a short cache on the
  two HTML files;
- the console's security headers, including
  `Content-Security-Policy: default-src 'none'; script-src 'self' https://in.veristar.app; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self' https://in.veristar.app; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`.
  The build has no inline script or style, so this policy holds.

The site sets no cookie. The only other origin it loads from is the Veristar gateway, for its own tracker.

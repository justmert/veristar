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

`dist/` is a plain static directory: `index.html`, `404.html`, `favicon.svg` and hashed files in
`assets/`. The console image ([`deploy/images/console.Dockerfile`](../../deploy/images/console.Dockerfile))
builds it and carries it at `/srv/site`, and every node's core Caddy serves it
([`deploy/caddy/app.Caddyfile`](../../deploy/caddy/app.Caddyfile), `SITE_ROOT`) at `<BASE_DOMAIN>`, with
`www.<BASE_DOMAIN>` redirecting there with 308. The image build passes the build arguments
`SITE_ORIGIN` and `REPO_URL` on as `VITE_SITE_ORIGIN` and `VITE_REPO_URL`; an empty one stays unset.

The Caddy site sends:

- `404.html` with status 404 for every path that is not a file;
- `Cache-Control: public, max-age=31536000, immutable` on `assets/*`, and
  `public, max-age=300` on every other file;
- the security headers of the console, with this policy:
  `Content-Security-Policy: default-src 'self'; script-src 'self' https://in.<BASE_DOMAIN>; connect-src 'self' https://in.<BASE_DOMAIN>; style-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`.
  The build has no inline script or style, so this policy holds. `in.<BASE_DOMAIN>` is the
  gateway, for the Veristar tracker.

Traffic Manager sends the apex and `www` to every healthy node (profile `site`,
[docs/deploy/opentofu.md](../../docs/deploy/opentofu.md)). Each node's port 80 answers `/site`
with 308 while its Caddy runs, because the site needs no other service.

The site sets no cookie.

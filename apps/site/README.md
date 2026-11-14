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
host) those links are hidden, except sign-in when the build sets `VITE_CONSOLE_ORIGIN`:

```sh
VITE_CONSOLE_ORIGIN=https://app.example.org pnpm --filter @veristar/site build
```

The source repository is not linked while it is private.

## Serving

`dist/` is a plain static directory: `index.html`, `404.html`, `favicon.svg` and hashed files in
`assets/`. Any static file server works. It belongs on the core tier beside the console, served
by a Caddy site for `<BASE_DOMAIN>` and `www.<BASE_DOMAIN>`, with:

- `404.html` answered with status 404 for every path that is not a file;
- `Cache-Control: public, max-age=31536000, immutable` on `assets/*`, and a short cache on the
  two HTML files;
- the console's security headers, including
  `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`.
  The build has no inline script or style, so this policy holds.

The site sets no cookie, loads nothing from another origin and sends no analytics.

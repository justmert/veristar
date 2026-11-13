# Verify the tracker

How a tenant checks that the `sdk.js` their pages load is exactly the released file, and how to
host it themselves (plan phases 3 and 18). Examples use version `1.1.0`; replace it with the version
in your snippet. `in.<BASE_DOMAIN>` is the platform's ingest host.

## 1. What protects you without any action

The console's snippet pins one version and its hash:

```html
<script defer src="https://in.<BASE_DOMAIN>/v/1.1.0/sdk.js"
  integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq"
  crossorigin="anonymous" data-website-id="<site id>"></script>
```

- The browser runs the file only if its SHA-384 matches `integrity`. A changed file is blocked,
  not run (tested in Chromium, Firefox and WebKit in this stage).
- Released files never change. `packages/tracker/releases/<version>/sdk.js` and its manifest entry
  are immutable: CI fails if either changes, and every release rebuilds the newest file and compares
  it byte for byte. A new tracker is a new version and a new snippet; nothing updates by itself.
- A version with a problem is listed in `packages/tracker/releases/revoked.json` (empty today). The
  console refuses to show a snippet for a revoked version.

## 2. Check a file by hand

```sh
curl -fsS -o sdk.js https://in.<BASE_DOMAIN>/v/1.1.0/sdk.js
# the integrity value of the snippet
printf 'sha384-%s\n' "$(openssl dgst -sha384 -binary sdk.js | base64)"
# the hex digest, as in releases/manifest.json
sha384sum sdk.js
```

Compare both with the entry for the version in
`https://github.com/justmert/stellar-analytics/blob/main/packages/tracker/releases/manifest.json`
(fields `integrity` and `sha384`).

## 3. Check where it was built

Each tracker release is attested by the release workflow (`release-tracker.yml`). With the GitHub
CLI:

```sh
gh attestation verify sdk.js --repo justmert/stellar-analytics
```

This checks a Sigstore-signed provenance statement saying that this exact file was produced by a
workflow run in this repository, at which commit and by which workflow file.

**Status on 2026-11-13: not yet available.** No tracker release has been attested, because GitHub
Actions fails at startup on the repository owner's account and the repository is still private
(attestations for private repositories need GitHub Enterprise Cloud). Until then, use sections 2
and 4.

## 4. Rebuild it yourself

The build is reproducible: the same source and the pinned esbuild (0.28.2) give the same bytes.

```sh
git clone https://github.com/justmert/stellar-analytics && cd stellar-analytics
git checkout tracker-v1.1.0          # or the commit that added releases/1.1.0
corepack disable; npm install -g pnpm@12.18.0
pnpm install --frozen-lockfile
pnpm --filter tracker build 1.1.0     # writes packages/tracker/dist/sdk.js
cmp packages/tracker/dist/sdk.js packages/tracker/releases/1.1.0/sdk.js && echo identical
```

Node.js 26.13.1 is the version the releases were built with.

## 5. Host it yourself

You may serve `sdk.js` from your own origin. Use the same bytes and keep the same `integrity`
value. Set `data-host-url` to the platform's ingest origin, because the script otherwise sends
events to the origin it was loaded from:

```html
<script defer src="/static/sdk-1.1.0.js"
  integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq"
  crossorigin="anonymous" data-website-id="<site id>"
  data-host-url="https://in.<BASE_DOMAIN>"></script>
```

Events always go from the browser straight to `in.<BASE_DOMAIN>`. A server-side relay of events is
not supported: it would turn every visitor into one address, which the same-IP checks treat as
suspicious ([bot-and-inflation.md](bot-and-inflation.md)).

## 6. The npm package

The same release is published to npm for bundler users (an ESM `init({ siteId, host })` entry, type
declarations, and the release files under `releases/`). It is published through npm trusted
publishing, so the package page shows provenance linking it to `release-tracker.yml` in this
repository, and `npm audit signatures` in your project checks the registry signatures and
provenance. Your lockfile pins the version.

**Status on 2026-11-13: not published.** The npm scope an operator input has not been supplied, and
trusted publishing needs a public repository and working GitHub Actions. The package build exists
(`packages/tracker/scripts/pack.mjs`) and is tested.

---
title: "Verify the tracker"
description: "Check that the sdk.js your pages load is exactly the released file, rebuild it, or host it yourself."
---

This page shows how you check that the `sdk.js` your pages load is exactly the released file, how
you rebuild it from source, and how you host it yourself. The examples use version `1.4.0`;
replace it with the version in your snippet. `in.<BASE_DOMAIN>` is the platform's ingest host.

## 1. What protects you without any action

The snippet the console gives you pins one version and its hash:

```html
<script defer src="https://in.<BASE_DOMAIN>/v/1.4.0/sdk.js"
  integrity="sha384-8bYExYW1FMGfNg5LETdGYFjtJPLfyY+gJQwWA0rL5xNM7Xl+3wK99GxSV3+siv/A"
  crossorigin="anonymous" data-website-id="<site id>"></script>
```

- The browser runs the file only if its SHA-384 hash matches `integrity`. A changed file is
  blocked and does not run. The end-to-end suite checks this in Chromium, Firefox and WebKit
  (`tests/e2e/specs/sri.spec.ts`).
- Released files never change. `packages/tracker/releases/<version>/sdk.js` and its entry in
  `packages/tracker/releases/manifest.json` are immutable: the `releases` check in
  `.github/workflows/tracker.yml` fails if either changes, and every release rebuilds the newest
  file and compares it byte for byte. A new tracker is a new version with a new snippet; nothing
  updates by itself.
- A version with a problem is listed in `packages/tracker/releases/revoked.json`. The console
  then refuses to show a snippet for it, and the gateway answers 410 for its `sdk.js`.
- The gateway serves `sdk.js` with `Cache-Control: public, max-age=31536000, immutable`, and you
  can host the file yourself, so a page can keep running a revoked release from its cache or its
  own copy. From release 1.2.0 on, every event names its release (field `sv`,
  [../reference/wire-v1.md](../reference/wire-v1.md)), and the gateway refuses events from a
  revoked release with 410 (`rejected_revoked_version`). Releases 1.0.0 and 1.1.0 do not send
  `sv`, so revoking one of them stops new loads of the file but not its events.

## 2. Check a file by hand

```sh
curl -fsS -o sdk.js https://in.<BASE_DOMAIN>/v/1.4.0/sdk.js
# the integrity value of the snippet
printf 'sha384-%s\n' "$(openssl dgst -sha384 -binary sdk.js | base64)"
# the hex digest, as in releases/manifest.json
sha384sum sdk.js
```

Compare both values with the fields `integrity` and `sha384` of the version's entry in
[packages/tracker/releases/manifest.json](https://github.com/justmert/veristar/blob/main/packages/tracker/releases/manifest.json).

## 3. Rebuild it yourself

The build is reproducible: the same source and the pinned esbuild (0.28.2) give the same bytes.
The newest release rebuilds from `main`; an older release rebuilds from the commit that added it.

```sh
git clone https://github.com/justmert/veristar && cd veristar
# for an older release, check out the commit that added it:
#   git checkout "$(git log --diff-filter=A --format=%H -1 -- packages/tracker/releases/<version>/sdk.js)"
corepack disable; npm install -g pnpm@12.18.0
pnpm install --frozen-lockfile
pnpm --filter tracker build 1.4.0     # writes packages/tracker/dist/sdk.js
cmp packages/tracker/dist/sdk.js packages/tracker/releases/1.4.0/sdk.js && echo identical
```

The release workflow builds with Node.js 26.13.1.

## 4. Host it yourself

You can serve `sdk.js` from your own origin. Use the same bytes and keep the same `integrity`
value. Set `data-host-url` to the platform's ingest origin, because otherwise the script sends
events to the origin it was loaded from:

```html
<script defer src="/static/sdk-1.4.0.js"
  integrity="sha384-8bYExYW1FMGfNg5LETdGYFjtJPLfyY+gJQwWA0rL5xNM7Xl+3wK99GxSV3+siv/A"
  crossorigin="anonymous" data-website-id="<site id>"
  data-host-url="https://in.<BASE_DOMAIN>"></script>
```

Events always go from the browser straight to `in.<BASE_DOMAIN>`. Do not relay events through
your own server: every visitor would then share your server's address, which the same-IP checks
treat as suspicious ([same-network skepticism](bot-and-inflation.md#8-same-network-skepticism-and-the-quarantine-band)).

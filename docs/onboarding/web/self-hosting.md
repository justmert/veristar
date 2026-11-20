---
title: "Self-hosting sdk.js"
description: "Serve the tracker file from your own domain while events still go to the gateway."
icon: "hard-drive"
---

You can serve the tracker file from your own domain instead of the gateway, for example to keep
every script of your site on your own origin. Tested by the page
[`self-hosted.html`](https://github.com/justmert/veristar/blob/main/examples/static-html/src/self-hosted.html) of the static example,
which runs in the browser test matrix.

Only the file moves. **Events still go from the visitor's browser directly to the gateway**,
`https://in.analytics.example` in these examples.

## Why events go straight to the gateway

A proxy on your domain that forwards events to the gateway would turn every visitor's event into a
request from your server's single address. The gateway counts unique visitors from the address and
browser of each request, checks that each event comes from a browser that solved its own challenge,
and quarantines addresses that show many different visitors within an hour. Behind a proxy, all
your visitors would share one address: they would be merged or quarantined, and the gateway could
no longer tell your real traffic from a script posting events. So the service accepts events only
from browsers, and only the file can be self-hosted.

## Steps

1. **Copy the file of the version you use.** Download it from the gateway, for example
   `https://in.analytics.example/v/1.4.0/sdk.js`, or take `packages/tracker/releases/1.4.0/sdk.js`
   from the repository. The files are immutable: a version's bytes never change.
2. **Serve it unchanged** from a path of your choice, for example `/js/sdk/1.4.0/sdk.js`. Do not
   minify, bundle or otherwise rewrite it; the integrity check would refuse it.
3. **Change the tag:** point `src` at your copy, keep the `integrity` value, and add
   `data-host-url` with the gateway origin, because `sdk.js` can no longer find the gateway from its
   own address:

{/* snippet version=1.4.0 self-hosted=/js/sdk/1.4.0/sdk.js */}
```html
<script defer src="/js/sdk/1.4.0/sdk.js" integrity="sha384-8bYExYW1FMGfNg5LETdGYFjtJPLfyY+gJQwWA0rL5xNM7Xl+3wK99GxSV3+siv/A" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c" data-host-url="https://in.analytics.example"></script>
```

4. **Check the file** before you publish it: its SHA-384 must be the integrity value in the
   console. [`docs/security/verify-tracker.md`](../../security/verify-tracker.md) shows how.

Your content security policy must still allow the gateway in `connect-src`
([content-security-policy.md](content-security-policy.md)).

## What you take on

- **Revocation does not reach your copy.** When a tracker version is revoked (because of a security
  problem), the gateway stops serving it, so tags that load it from the gateway stop running at
  once. A copy on your server keeps running until you replace it. Watch the release notes and the
  revocation list ([`packages/tracker/releases/revoked.json`](https://github.com/justmert/veristar/blob/main/packages/tracker/releases/revoked.json))
  and upgrade the same day a version you host is revoked.
- **Upgrades are yours.** Each new version is a new file, a new path and a new integrity value;
  [upgrading.md](upgrading.md) applies unchanged.
- **Caching.** The gateway serves the file with a one year immutable cache lifetime. Serving a
  versioned path lets you do the same.

## What the browser tests check

On the self-hosted page: `sdk.js` is loaded from the site, the gateway never serves it, and the
pageview reaches the gateway with a valid proof from the page's own origin, in Chromium, Firefox
and WebKit (`tests/e2e/specs/onboarding.spec.ts`). The example's build test checks that the copied
file matches the release's integrity value.

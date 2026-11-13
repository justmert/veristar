---
title: "The npm package"
sidebarTitle: "npm package"
description: "Bundle the tracker into your app as an npm package instead of a script tag."
---

Instead of a script tag, the tracker can be bundled into your app as an npm package. It is the
same tracker: `init()` installs `window.umami` exactly as `sdk.js` does, so `umami.track()` and
`data-umami-event` work unchanged.

**Status: not published yet.** The package is built by
[`packages/tracker/scripts/pack.mjs`](https://github.com/justmert/veristar/blob/main/packages/tracker/scripts/pack.mjs) under the npm
scope the service will publish it to, which is still being set up an operator input of the project plan).
Until it is published, the console offers only the script tag, and that is what you should use.
This page describes the package as it is built and tested today, so you can plan for it.

## Script tag or package

| | Script tag | npm package |
|---|---|---|
| Version pinned by | the URL and the integrity value in your HTML | your lockfile |
| Code reviewed by | the browser, on every load (integrity check) | your package manager, at install (lockfile integrity) |
| Runs | from the gateway, or from your own server ([self-hosting.md](self-hosting.md)) | inside your bundle |
| Wallets Kit network in `wallet_connected` | no | yes ([wallets-kit.md](wallets-kit.md)) |

Both send the same events to the same gateway. The package carries the immutable release files
too: `releases/<version>/sdk.js` inside the package is byte for byte the file the gateway serves.

## Using it

Install the exact version (`--save-exact`, or the equivalent of your package manager), never a
range, then call `init()` once in your app's entry module, before anything renders:

{/* include examples/wallets-kit-spa/src/module-page.tsx region=init */}
```tsx
init({ siteId: __SA_SITE_ID__, host: __SA_GATEWAY_ORIGIN__ });
```

`siteId` is your site id from the console and `host` the gateway origin, for example
`https://in.analytics.example`. The example passes them in as build-time constants so tests can
choose their own; in your app write them as plain strings. The example imports `init` from the
workspace package `tracker`; the published package has a scoped name, which the console will show.

`init()` returns the tracker. Calling it again returns the same tracker; if a script tag already
installed `window.umami`, `init()` returns that one, so the two never count twice.

`init()` takes the same options as the tag's attributes, in camel case: `autoTrack`, `doNotTrack`,
`excludeSearch`, `excludeHash`, `domains` (an array), `beforeSend` (a function) and `walletsKit`
(use `attachWalletsKit` instead, see [wallets-kit.md](wallets-kit.md)).

## Building against the source package

Inside this repository the examples use the workspace package, which exports the TypeScript
sources. Those expect the bundler to define `__SDK_VERSION__`, as
[`examples/wallets-kit-spa/vite.config.ts`](https://github.com/justmert/veristar/blob/main/examples/wallets-kit-spa/vite.config.ts) does.
The package built by `pack.mjs` has the version compiled in and needs no such setting.

## What is tested

The npm entry is exercised by the Wallets Kit example's module page, which `TestWalletsKit` in
`tests/webpath` runs through the real gateway into Umami. `pack.mjs` refuses to pack a version that is
not in the release manifest, copies the release files unchanged and checks that no built file
still needs `__SDK_VERSION__`.

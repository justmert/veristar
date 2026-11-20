---
title: "The npm package"
sidebarTitle: "npm package"
description: "Bundle the tracker into your app as an npm package instead of a script tag."
icon: "npm"
iconType: "brands"
---

The tracker also comes as an npm package that you bundle into your app instead of loading a script
tag. It is the same tracker: `init()` installs `window.veristar` and `window.umami` exactly as
`sdk.js` does, so `veristar.track()`, `umami.track()` and `data-umami-event` work unchanged.

You build the npm package from this repository with
[`packages/tracker/scripts/pack.mjs`](https://github.com/justmert/veristar/blob/main/packages/tracker/scripts/pack.mjs),
under a scoped package name of your choice, and publish it to a registry you control. When the
service you use publishes the releases under a scoped name (the control plane setting
`TRACKER_NPM_PACKAGE`), the site's setup page in the console has an **npm** choice under **How is the site built?** with the exact
`npm install --save-exact` command, the import and the `init()` call for your site.

## Script tag or package

| | Script tag | npm package |
|---|---|---|
| Version pinned by | the URL and the integrity value in your HTML | your lockfile |
| Code reviewed by | the browser, on every load (integrity check) | your package manager, at install (lockfile integrity) |
| Runs | from the gateway, or from your own server ([self-hosting.md](self-hosting.md)) | inside your bundle |
| Wallets Kit network in `wallet_connected` | no | yes ([wallets-kit.md](wallets-kit.md)) |

Both send the same events to the same gateway. The package carries the immutable release files
too: `releases/<version>/sdk.js` inside the package is byte for byte the file the gateway serves.

## Build the package

From a checkout of the repository, with the pnpm version from `package.json` installed:

```sh
pnpm install --frozen-lockfile
cd packages/tracker
NPM_PACKAGE=@your-scope/veristar-tracker node scripts/pack.mjs 1.4.0
```

`NPM_PACKAGE` is the scoped name the package gets, and the argument is the tracker version. The
script compiles the package code from the checkout's source, so it packs only the version in
`packages/tracker/package.json` (1.4.0 on `main`); without an argument it uses that version. To
pack an earlier release, check out the commit that released it. The version must be listed in
[`releases/manifest.json`](https://github.com/justmert/veristar/blob/main/packages/tracker/releases/manifest.json).
The script prints, as JSON, the path of the tarball, for example
`npm/your-scope-veristar-tracker-1.4.0.tgz`. Install that file in your app with your package
manager, or publish it to a registry you control.

`pack.mjs` refuses a version that is not in the release manifest or is not the source's own, copies
the release files unchanged and checks that no built file still needs `__SDK_VERSION__`. Two runs
on the same source produce the same tarball.

## Initialize the tracker

Pin the exact version (`--save-exact`, or the equivalent of your package manager), never a range.
Then call `init()` once in your app's entry module, before anything renders:

{/* include examples/wallets-kit-spa/src/module-page.tsx region=init */}
```tsx
init({ siteId: __SA_SITE_ID__, host: __SA_GATEWAY_ORIGIN__ });
```

`siteId` is your site id from the console and `host` the gateway origin, for example
`https://in.analytics.example`. The example passes them in as build-time constants so tests can
choose their own; in your app write them as plain strings. The example imports `init` from the
workspace package `tracker`; in your app, import it from the scoped name you packed, for example
`@your-scope/veristar-tracker`.

`init()` returns the tracker. Calling it again returns the same tracker; if a script tag already
installed `window.veristar` or `window.umami`, `init()` returns that one, so the two never count
twice.

`init()` takes the same options as the tag's attributes, in camel case: `autoTrack`, `doNotTrack`,
`excludeSearch`, `excludeHash`, `domains` (an array), `beforeSend` (a function), `sendTitle` and
`walletsKit` (use `attachWalletsKit` instead, see [wallets-kit.md](wallets-kit.md)).
With `autoTrack: false` the tracker waits for `start()`, after consent, before it sends anything,
`track()` calls and wallet events included (from 1.3.1). Call `start({ automatic: false })` to send
only what you pass to `track()`, without automatic pageviews and click capture
([consent.md](consent.md)).

## Build against the workspace package

Inside this repository the examples use the workspace package, which exports the TypeScript
sources. Those expect the bundler to define `__SDK_VERSION__`, as
[`examples/wallets-kit-spa/vite.config.ts`](https://github.com/justmert/veristar/blob/main/examples/wallets-kit-spa/vite.config.ts) does.
The package that `pack.mjs` builds has the version compiled in and needs no such setting.

## What is tested

The Wallets Kit example's module page uses the package entry. `TestWalletsKit` in `tests/webpath`
runs that page through the real gateway into Umami.

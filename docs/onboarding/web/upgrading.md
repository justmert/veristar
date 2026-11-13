---
title: "Upgrading the tracker"
description: "Move your pages to a newer pinned tracker version, at the time you choose."
---

The tag pins one tracker version, and the browser checks the file against the tag's `integrity`
value. Nothing on your pages changes unless you change the tag: the service has no "latest" URL and
never updates a released file. This page explains how to move to a newer version.

## What a version is

Each release is one file, `https://in.analytics.example/v/<version>/sdk.js`, and one SHA-384 hash.
The list of releases, with their sizes and hashes, is
[`packages/tracker/releases/manifest.json`](https://github.com/justmert/veristar/blob/main/packages/tracker/releases/manifest.json); a
released file is never rebuilt or replaced. Released so far:

| Version | Adds |
|---|---|
| 1.0.0 | the first release |
| 1.1.0 | Stellar Wallets Kit capture with `data-wallets-kit="true"` ([wallets-kit.md](wallets-kit.md)) |
| 1.2.0 | long single-page sessions keep sending (a new challenge after 40 events, and once more after a "proof used up" answer; 1.0.0 and 1.1.0 lose events after the 50th per page load and node); back and forward in Next.js keep the previous page as referrer; event data Umami cannot store is refused in the browser with a clear error (numbers from 1e15, revenue from 1e14 with a currency, dates before year 1, names starting with `=`, `+`, `-` or `@`); Wallets Kit storage is never read for a visitor with Global Privacy Control or the opt-out flag, even after `umami.start()`; every event names its release, so events of a revoked release are refused |

## Steps

1. Open the site's **Snippet** page in the console and choose the new version in **Tracker
   version**. The newest is marked. The page shows the new tag, its release date and its size.
2. Replace the whole tag on your pages with the new one. Both `src` and `integrity` change; copying
   only one of them makes the browser refuse the file. Keep any attributes you added yourself
   (`data-auto-track`, `data-domains` and so on).
3. Deploy, then check one page as in the [quickstart](quickstart.md#5-check-that-it-works):
   `sdk.js` with the new version answers 200 and the pageview answers 202.

Moving from 1.0.0 to 1.1.0 changes only these two attributes:

{/* snippet version=1.0.0 */}
```html
<script defer src="https://in.analytics.example/v/1.0.0/sdk.js" integrity="sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

{/* snippet version=1.1.0 */}
```html
<script defer src="https://in.analytics.example/v/1.1.0/sdk.js" integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

Versions can be mixed: pages still carrying the old tag keep working while you roll out the new
one, and all pages count into the same dashboard.

For the [React Router](react-router.md) and [Next.js](nextjs.md) layouts, update the `src` and
`integrity` values in the element. With the [npm package](npm.md), install the new exact version.
A [self-hosted](self-hosting.md) copy needs the new file at a new path as well.

## Checking a file yourself

Before you trust a new version, you can compare the file with the published hash and, once the
release pipeline signs releases, with its signature:
[`docs/security/verify-tracker.md`](../../security/verify-tracker.md).

## Revoked versions

If a released version turns out to have a security problem, it is revoked: the gateway answers
`410 Gone` for its file, the console stops offering it, and pages that still load it from the
gateway send nothing until their tag is replaced. A revoked version is listed in
[`revoked.json`](https://github.com/justmert/veristar/blob/main/packages/tracker/releases/revoked.json) with the reason. No version has
been revoked so far.

Because the tracker is pinned, the service cannot fix a problem on your pages without you: watch
the repository's releases and replace the tag when a version you use is revoked.

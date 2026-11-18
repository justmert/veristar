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
| 1.3.0 | counts only pages a person can see: a prerendered page counts when it is activated, a page opened in a background tab when it is first shown, a page restored from the back/forward cache counts again, and single-page navigation keeps counting; events of a page inside a frame say so, and a page left without ever being shown sends a never-visible notice instead of a page view (both feed the [integrity grade](../../security/bot-and-inflation.md)); screen size, language and page title are no longer read or sent, and `data-send-title="true"` sends the title for a site that wants it |
| 1.3.1 | with `data-auto-track="false"`, `umami.track()` before `umami.start()` sends nothing, fetches no challenge and reads no browser storage, so code that runs before the consent banner is answered cannot measure anyone; a site that sends only chosen events calls `umami.start({ automatic: false })` once, which starts `umami.track()` without automatic pageviews or click capture ([consent.md](consent.md)) |
| 1.3.2 | a route that redirects at once counts as one pageview of where the visitor lands, instead of two; `umami.start()` counts the page the visitor is on when consent is given after single-page navigation, and in manual mode every `umami.track()` names the current page; a tracked link that a client-side router handles stays a client-side navigation, and a plain same-tab link is held for its event for at most one second; download links and links into another window are left to the browser; nul characters in the title or referrer are dropped instead of the event being refused |

## Steps

1. Open the site's setup page in the console (**Snippet** on the site's **Setup** tab). Under
   **Version and what the tag pins**, choose the new version in **Tracker version**; the newest is
   marked recommended. The page shows the new tag, its release date and its size.
2. Replace the whole tag on your pages with the new one. Both `src` and `integrity` change; copying
   only one of them makes the browser refuse the file. Keep any attributes you added yourself
   (`data-auto-track`, `data-domains` and so on).
3. Deploy, then check one page as in the [quickstart](quickstart.md#5-check-that-it-works):
   `sdk.js` with the new version answers 200 and the pageview answers 202.

Moving from 1.3.1 to 1.3.2 changes only these two attributes:

{/* snippet version=1.3.1 */}
```html
<script defer src="https://in.analytics.example/v/1.3.1/sdk.js" integrity="sha384-54fz21LNRgy6RLEdhwecMK8yavckH44JnSX9iSA3p2uiEtCEs6fyFt6LhuOL5kKw" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

{/* snippet version=1.3.2 */}
```html
<script defer src="https://in.analytics.example/v/1.3.2/sdk.js" integrity="sha384-Q2xIA5ztfj7VBlpI/+6kehWQVUC2jZPr89aAooy7Uinc/V2C0VF7Ti66L3gj6D/v" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

Versions can be mixed: pages still carrying the old tag keep working while you roll out the new
one, and all pages count into the same dashboard.

For the [React Router](react-router.md) and [Next.js](nextjs.md) layouts, update the `src` and
`integrity` values in the element. With the [npm package](npm.md), build and install the new exact version.
A [self-hosted](self-hosting.md) copy needs the new file at a new path as well.

## Checking a file yourself

Before you trust a new version, compare the file with the published hash or rebuild it from
source: [Verify the tracker](../../security/verify-tracker.md).

## Revoked versions

If a released version turns out to have a security problem, it is revoked: the gateway answers
`410 Gone` for its file, the console stops offering it, and pages that still load it from the
gateway send nothing until their tag is replaced. A revoked version is listed in
[`revoked.json`](https://github.com/justmert/veristar/blob/main/packages/tracker/releases/revoked.json) with the reason. No version has
been revoked so far.

Because the tracker is pinned, the service cannot fix a problem on your pages without you: watch
the repository's releases and replace the tag when a version you use is revoked.

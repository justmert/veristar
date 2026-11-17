---
title: "VitePress"
description: "Add the tracker to a VitePress documentation site with the head option."
---

For a VitePress site. Tested by the example
[`examples/vitepress`](https://github.com/justmert/veristar/tree/main/examples/vitepress)
(VitePress 1.6.4 with the default theme, `vitepress build`, with the built pages served as static
files), which runs in the browser test matrix.

## Where the tag goes

Add the tag to the `head` option of `.vitepress/config.ts` (or `config.mts`, `config.js`).
VitePress renders every entry of `head` into the `<head>` of every page it builds:

{/* include examples/vitepress/.vitepress/config.ts region=head */}
```ts
// rendered into the <head> of every built page; an empty string is an
// attribute without a value, as defer is in the console's tag
head: [
  [
    "script",
    {
      defer: "",
      src: tracker.src,
      integrity: tracker.integrity,
      crossorigin: "anonymous",
      "data-website-id": tracker.siteId,
    },
  ],
],
```

The example reads the three values at build time from its environment and the tracker's release
manifest, so tests can use their own site id:

{/* include examples/vitepress/.vitepress/tracker.ts */}
```ts
// the tracker tag's attributes, read at build time from the environment and
// the release manifest, so tests can use their own site id and gateway. a
// tenant writes the values from the console into .vitepress/config.ts instead.
import manifest from "../../../packages/tracker/releases/manifest.json" with { type: "json" };

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

export function trackerTag(): { src: string; integrity: string; siteId: string } {
  const version = required("SA_TRACKER_VERSION");
  const entry = manifest.versions.find((r) => r.version === version);
  if (!entry) throw new Error(`tracker version ${version} is not in the release manifest`);
  return {
    src: `${required("SA_GATEWAY_ORIGIN")}/v/${version}/sdk.js`,
    integrity: entry.integrity,
    siteId: required("SA_SITE_ID"),
  };
}
```

In your site, write the values from the console's tag directly into the entry: `src`, `integrity`
and `data-website-id` of

{/* snippet version=1.3.0 */}
```html
<script defer src="https://in.analytics.example/v/1.3.0/sdk.js" integrity="sha384-3E+vbTmGHWaU8kaLAWT6ttw70odRH565/3vRal5558SgSitqFlIbsFHlfzWnSUtT" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

With the entry above, VitePress writes exactly the console's tag into every built page.

## Route changes

VitePress builds a single-page app. The first page a visitor opens is a full load and one
pageview. After that, VitePress's router takes over clicks on links inside the site and changes the
address with `history.pushState`, which the tracker watches, so each is one pageview, and back and
forward are one each. You need no router hook. A jump to a heading anchor on the same page is no
pageview.

The example turns on `cleanUrls`, so pages report `/pricing` instead of `/pricing.html`. Without
it, the dashboard lists the `.html` paths.

## Custom events

A `data-umami-event` attribute works in Markdown pages, which accept HTML:

{/* include examples/vitepress/index.md region=event */}
```html
<button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
  Sign up
</button>
```

From a Vue component, call `window.umami?.track()`; the visitor can block the tracker, so the call
is optional:

{/* include examples/vitepress/.vitepress/theme/TrackButton.vue region=track */}
```vue
// the tracker may be blocked by the visitor, so the call is optional
function manualCall() {
  void window.umami?.track("manual-call", { source: "vitepress" });
}
```

The example registers its components in `.vitepress/theme/index.ts`, which extends the default
theme.

## Content security policy

VitePress sends no `Content-Security-Policy` of its own. If your host adds one, allow the gateway
as [content-security-policy.md](content-security-policy.md) describes.

## What the browser tests check

The same matrix as for every client-routed example, in Chromium, Firefox and WebKit: one pageview
per route change, back and forward, the previous route as referrer, an external referrer on the
first pageview, both kinds of custom event, no request with Global Privacy Control and a tampered
tracker refused (`tests/e2e/specs`). The example's build test checks that every built page carries
the console's tag once (`examples/vitepress/test`).

---
title: "Docusaurus"
description: "Add the tracker to a Docusaurus documentation site with the scripts option."
icon: "book-open"
---

For a Docusaurus 3 site. Tested by the example
[`examples/docusaurus`](https://github.com/justmert/veristar/tree/main/examples/docusaurus)
(Docusaurus 3.10.2 with the classic preset, React 19.3.1, `docusaurus build` and
`docusaurus serve`), which runs in the browser test matrix.

## Where the tag goes

Add the tag to the `scripts` option of `docusaurus.config.ts` (or `docusaurus.config.js`).
Docusaurus writes every entry of `scripts` as a `<script>` element into the `<head>` of every page
it builds, and every key of the entry becomes an attribute:

{/* include examples/docusaurus/docusaurus.config.ts region=scripts */}
```ts
// every key of the object becomes an attribute of the <script> element in
// the <head> of every page, so the tag reaches the browser as the console
// wrote it, with its integrity value
scripts: [
  {
    src: tracker.src,
    defer: true,
    integrity: tracker.integrity,
    crossorigin: "anonymous",
    "data-website-id": tracker.siteId,
  },
],
```

The example reads the three values at build time from its environment and the tracker's release
manifest, so tests can use their own site id:

{/* include examples/docusaurus/tracker.ts */}
```ts
// the tracker tag's attributes, read at build time from the environment and
// the release manifest, so tests can use their own site id and gateway. a
// tenant writes the values from the console into docusaurus.config.ts instead.
import manifest from "../../packages/tracker/releases/manifest.json" with { type: "json" };

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

{/* snippet version=1.4.0 */}
```html
<script defer src="https://in.analytics.example/v/1.4.0/sdk.js" integrity="sha384-8bYExYW1FMGfNg5LETdGYFjtJPLfyY+gJQwWA0rL5xNM7Xl+3wK99GxSV3+siv/A" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

Keep `defer: true` and `crossorigin: "anonymous"` as they are. Docusaurus writes `defer` as
`defer="defer"`, which the browser reads exactly like `defer`. Use `scripts`, not a client module
that creates the element: the entry reaches the built HTML with its `integrity` value, so the
browser refuses a changed file before it runs.

## Route changes

Docusaurus builds a single-page app. The first page a visitor opens is a full load and one
pageview. After that, links inside the site (the navbar, the sidebar, `<Link>` and Markdown links)
change the address with `history.pushState` through React Router, which the tracker watches, so
each is one pageview, and back and forward are one each. You need no client module and no
`onRouteDidUpdate` hook. A jump to a heading anchor on the same page is no pageview.

The example puts its links in a component that every page renders:

{/* include examples/docusaurus/src/components/Nav.tsx */}
```tsx
import Link from "@docusaurus/Link";

// links inside the site go through docusaurus's router, which changes the
// url with history.pushState
export default function Nav() {
  return (
    <nav>
      <Link to="/" id="nav-home">
        Home
      </Link>{" "}
      <Link to="/pricing" id="nav-pricing">
        Pricing
      </Link>{" "}
      <Link to="/docs/getting-started" id="nav-docs">
        Docs
      </Link>
    </nav>
  );
}
```

Docusaurus's `trailingSlash` option decides whether links end in a slash. The dashboard lists
`/docs/intro` and `/docs/intro/` as two pages, so set `trailingSlash` to `true` or `false` to have
full loads and client-side navigations report the same form.

## Custom events

A `data-umami-event` attribute works in MDX pages as it does in HTML:

{/* include examples/docusaurus/src/pages/index.mdx region=event */}
```mdx
<button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
  Sign up
</button>
```

From React code, call `window.umami?.track()` in a component; the visitor can block the tracker,
so the call is optional:

{/* include examples/docusaurus/src/components/TrackButton.tsx region=track */}
```tsx
// the tracker may be blocked by the visitor, so the call is optional
export default function TrackButton() {
  const onClick = () => {
    void window.umami?.track("manual-call", { source: "docusaurus" });
  };
  return (
    <button type="button" id="manual" onClick={onClick}>
      Track by call
    </button>
  );
}
```

## Content security policy

Docusaurus sends no `Content-Security-Policy` of its own. If your host adds one, allow the gateway
as [content-security-policy.md](content-security-policy.md) describes.

## What the browser tests check

The same matrix as for every client-routed example, in Chromium, Firefox and WebKit: one pageview
per route change, back and forward, the previous route as referrer, an external referrer on the
first pageview, both kinds of custom event, no request with Global Privacy Control and a tampered
tracker refused (`tests/e2e/specs`). The example's build test checks that every built page carries
the tag with the release's integrity value once (`examples/docusaurus/test`).

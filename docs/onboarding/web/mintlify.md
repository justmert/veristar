---
title: "Mintlify"
description: "Add the tracker to a Mintlify documentation site with a custom JavaScript file."
---

For a documentation site hosted by Mintlify. Mintlify runs every `.js` file in your docs repository
on every page, on every plan including Free
([Custom scripts](https://www.mintlify.com/docs/customize/custom-scripts),
[pricing](https://www.mintlify.com/pricing)). One such file adds the tracker. Tested by the example
[`examples/mintlify`](https://github.com/justmert/veristar/tree/main/examples/mintlify), which runs
in the browser test matrix in Mintlify's local preview (`mint dev`, CLI 4.3.49).

## Where the tag goes

Create `tracker.js` next to `docs.json` in your docs repository:

{/* include examples/mintlify/site/tracker.js region=tracker */}
```js
const tracker = document.createElement("script");
tracker.src = "https://in.analytics.example/v/1.3.2/sdk.js";
tracker.integrity = "sha384-Q2xIA5ztfj7VBlpI/+6kehWQVUC2jZPr89aAooy7Uinc/V2C0VF7Ti66L3gj6D/v";
tracker.crossOrigin = "anonymous";
tracker.dataset.websiteId = "3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c";
document.head.appendChild(tracker);
```

Each line sets one attribute of the console's tag on a new `<script>` element:
`tracker.src` is `src`, `tracker.integrity` is `integrity`, `tracker.crossOrigin` is `crossorigin`
and `tracker.dataset.websiteId` is `data-website-id`. Replace the three example values with the
ones from your tag:

{/* snippet version=1.3.2 */}
```html
<script defer src="https://in.analytics.example/v/1.3.2/sdk.js" integrity="sha384-Q2xIA5ztfj7VBlpI/+6kehWQVUC2jZPr89aAooy7Uinc/V2C0VF7Ti66L3gj6D/v" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

The browser checks a script element created from JavaScript against its `integrity` value exactly
as it checks one written in HTML, and refuses a changed file before it runs. `defer` has no effect
on an element added after the page has loaded, so the file does not set it.

Mintlify asks you to load third-party scripts this way, from a `.js` file, not with a
`<script src>` tag in an MDX page. The `integrations` object in `docs.json` configures Mintlify's
built-in analytics providers only; the tracker needs none of it.

## Route changes

Mintlify runs `tracker.js` once per visit. After the first page, it swaps page content without a
reload and changes the address with `history.pushState`, which the tracker watches, so each page
change is one pageview, and back and forward are one each. You need no `mintlify:navigate`
listener. A jump to a heading anchor on the same page is no pageview.

## Custom events

A `data-umami-event` attribute works in MDX pages:

{/* include examples/mintlify/site/index.mdx region=event */}
```mdx
<button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
  Sign up
</button>
```

To send an event from code, call `window.umami?.track()` from a handler; the visitor can block the
tracker, so the call is optional:

{/* include examples/mintlify/site/index.mdx region=track */}
```mdx
{/* the tracker may be blocked by the visitor, so the call is optional */}
<button type="button" id="manual" onClick={() => window.umami?.track("manual-call", { source: "mintlify" })}>
  Track by call
</button>
```

## Content security policy

Sites hosted by Mintlify send a `Content-Security-Policy` whose `script-src` and `connect-src`
allow every `https:` origin, so the gateway needs no entry. If you serve the docs through your own
proxy with a stricter policy, allow the gateway as
[content-security-policy.md](content-security-policy.md) describes.

## What the browser tests check

The example's docs run in `mint dev`, the local preview of the Mintlify CLI, in the same matrix as
every client-routed example, in Chromium, Firefox and WebKit: one pageview per page change, back
and forward, the previous page as referrer, an external referrer on the first pageview, both kinds
of custom event, no request with Global Privacy Control and a tampered tracker refused
(`tests/e2e/specs`). The example's build test checks that `tracker.js` carries the values of the
console's tag for version 1.3.2 (`examples/mintlify/test`).

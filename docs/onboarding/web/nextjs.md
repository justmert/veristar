---
title: "Next.js"
description: "Add the tracker to a Next.js app that uses the App Router."
---

For Next.js with the App Router. Tested by the example [`examples/nextjs`](https://github.com/justmert/veristar/tree/main/examples/nextjs)
(Next.js 16.4.6, React 19.3.1, `next build` and `next start`), which runs in the browser test
matrix.

## Where the tag goes

Render the tag as a plain `<script>` element in the `<head>` of the root layout, `app/layout.js`
(or `app/layout.tsx`):

{/* include examples/nextjs/app/layout.js region=head */}
```js
{/* a plain script element, not next/script: the tag must reach the
    browser exactly as the console wrote it, with its integrity value */}
<script
  defer
  src={tracker.src}
  integrity={tracker.integrity}
  crossOrigin="anonymous"
  data-website-id={tracker.siteId}
/>
```

The example reads the three values on the server from its environment and the tracker's release
manifest, so tests can use their own site id:

{/* include examples/nextjs/app/tracker.js */}
```js
// the tracker tag's attributes, read on the server from the environment and
// the release manifest, so tests can use their own site id and gateway. a
// tenant writes the values from the console into app/layout.js instead.
import manifest from "tracker/releases/manifest.json";

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

export function trackerTag() {
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

In your app, write the values from the console's tag directly into the element: `src`,
`integrity` and `data-website-id` of

{/* snippet version=1.1.0 */}
```html
<script defer src="https://in.analytics.example/v/1.1.0/sdk.js" integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

Write `crossOrigin` in JSX; React renders it as `crossorigin`. The example is plain JavaScript only
because Next.js type-checks TypeScript projects through the `typescript` package's compiler API,
which the TypeScript 7 pinned in this repository does not offer; in a TypeScript app the layout is
the same.

## Route changes

The first load is server rendered and is one pageview. `<Link>` navigations happen on the client
with `history.pushState`, which the tracker watches, so each is one pageview; back and forward
too.

Right after a back or forward navigation, the App Router calls `history.replaceState` with the
current URL. Tracker 1.2.0 and newer ignore a call that does not change the URL, so the pageview
keeps the previous page as its referrer. With 1.0.0 and 1.1.0 the referrer of that pageview becomes
the page itself. The dashboards show the same numbers either way, because the gateway drops
referrers inside your own site (they arrive as paths); only a `data-before-send` hook that reads the
referrer sees the difference. The example pins 1.1.0, so its browser test checks the referrer on
forward navigations.

## Custom events

`data-umami-event` attributes work in server and client components alike. A click handler needs a
client component:

{/* include examples/nextjs/app/track-button.js region=track */}
```js
// a click handler needs a client component. the tracker may be blocked by the
// visitor, so the call is optional.
export function TrackButton() {
  return (
    <button
      type="button"
      id="manual"
      onClick={() => void window.umami?.track("manual-call", { source: "nextjs" })}
    >
      Track by call
    </button>
  );
}
```

## What the browser tests check

One pageview per server-rendered load and per client-side navigation, back and forward, the
previous route as referrer on forward navigations, an external referrer on the first pageview,
both kinds of custom event, Global Privacy Control and a tampered tracker refused
(`tests/e2e/specs`, `examples/nextjs/test`).

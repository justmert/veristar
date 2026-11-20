---
title: "Vite and React"
description: "Add the tracker to a client-routed React app built with Vite."
icon: "react"
iconType: "brands"
---

For a client-routed React app built with Vite. Tested by the example
[`examples/vite-react-spa`](https://github.com/justmert/veristar/tree/main/examples/vite-react-spa) (React 19, React Router 8 in data
mode, Vite 8), which runs in the browser test matrix.

## Where the tag goes

Put the tag in `index.html`, not in a component, so it loads once per page load:

{/* include examples/vite-react-spa/index.html region=head */}
```html
<!-- paste the tag from the console in place of the next line, once, in index.html -->
<!-- tracker -->
```

The example replaces the comment with the tag at build time so that tests can use their own site
id. In your app the tag from the console stands there:

{/* snippet version=1.4.0 */}
```html
<script defer src="https://in.analytics.example/v/1.4.0/sdk.js" integrity="sha384-8bYExYW1FMGfNg5LETdGYFjtJPLfyY+gJQwWA0rL5xNM7Xl+3wK99GxSV3+siv/A" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

Vite copies tags it does not process into the built `index.html` unchanged. Check `dist/index.html`
after `vite build` once.

## Route changes

The tracker sees every `history.pushState` and `replaceState` call and every back and forward
navigation, so each route change of React Router (or any other router) is one pageview. Nothing
needs to be added to the router.

## Custom events

`data-umami-event` attributes work on any element React renders. For a call from code, declare
`window.umami` once so TypeScript knows it:

{/* include examples/vite-react-spa/src/main.tsx region=types */}
```tsx
// window.umami as sdk.js installs it; optional because a visitor may block it
declare global {
  interface Window {
    umami?: {
      track: (name: string, data?: Record<string, string | number | boolean>) => Promise<void>;
    };
  }
}
```

and call it with optional chaining, because the visitor can block the tracker:

{/* include examples/vite-react-spa/src/main.tsx region=track */}
```tsx
function Home() {
  return (
    <>
      <h1>SPA example</h1>
      <p>Client-side routing. Every route change is one pageview.</p>
      <button
        type="button"
        id="signup"
        data-umami-event="signup-click"
        data-umami-event-plan="free"
      >
        Sign up
      </button>{" "}
      <button
        type="button"
        id="manual"
        // the tracker may be blocked by the visitor, so the call is optional
        onClick={() => void window.umami?.track("manual-call", { source: "vite-react-spa" })}
      >
        Track by call
      </button>
    </>
  );
}
```

## Hash routers

A router that keeps the route in the fragment (`/#/settings`) is counted too: fragments that start
with `#/` are sent without any query part inside them. Every other fragment is dropped, because a
fragment can carry an OAuth token.

## Bundling instead of a tag

To ship the tracker inside your bundle, see [npm.md](npm.md). The script tag is the simpler choice:
its version and integrity are fixed in one visible place.

## What the browser tests check

One pageview per route change, back and forward each counted, each pageview naming the previous
route as referrer, one challenge per page load shared by all its events, both kinds of custom event,
an external referrer kept on the first pageview, Global Privacy Control, and a tampered tracker
refused (`tests/e2e/specs`).

# React Router with server rendering

For React Router in framework mode (the successor of Remix), rendered on the server. Tested by the
example [`examples/react-router-ssr`](../../../examples/react-router-ssr) (React Router 8.4.0 with
`ssr: true`, served by `@react-router/serve`), which runs in the browser test matrix.

## Where the tag goes

Render the tag as a `<script>` element in the `<head>` of the root layout, `app/root.tsx`. React
writes it into the server-rendered HTML; on the client, React does not run it a second time.

In the example, the values come from the server's environment so tests can use their own site id:

<!-- include examples/react-router-ssr/app/root.tsx region=head -->
```tsx
{data ? (
  <script
    defer
    src={data.tracker.src}
    integrity={data.tracker.integrity}
    crossOrigin="anonymous"
    data-website-id={data.tracker.siteId}
  />
) : null}
```

The root loader reads them from the release manifest of the tracker package:

<!-- include examples/react-router-ssr/app/tracker.server.ts -->
```ts
// the tracker tag's attributes, read on the server for every request. they come
// from the same release manifest the snippet generator uses; react renders them
// as a script element, which is how a react router app carries the snippet.
import { releases, scriptUrl } from "tracker/snippet";

export type TrackerTag = { src: string; integrity: string; siteId: string };

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

export function trackerTag(): TrackerTag {
  const version = required("SA_TRACKER_VERSION");
  const entry = releases().find((r) => r.version === version);
  if (!entry) throw new Error(`tracker version ${version} is not in the release manifest`);
  return {
    src: scriptUrl(required("SA_GATEWAY_ORIGIN"), version),
    integrity: entry.integrity,
    siteId: required("SA_SITE_ID"),
  };
}
```

In your app, write the three values from the console's tag directly into the element instead:
`src`, `integrity` and `data-website-id` of

<!-- snippet version=1.1.0 -->
```html
<script defer src="https://in.analytics.example/v/1.1.0/sdk.js" integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

Write `crossOrigin` in JSX; React renders it as `crossorigin`.

## Route changes

The first load is server rendered and is one pageview. Later navigations happen on the client
with `history.pushState`, which the tracker watches, so each is one pageview; back and forward too.

## Custom events

Declare `window.umami` once and call it with optional chaining, as in the
[Vite and React guide](vite-react.md#custom-events); the example's
[`app/routes/home.tsx`](../../../examples/react-router-ssr/app/routes/home.tsx) does exactly that.

## What the browser tests check

One pageview per server-rendered load and per client-side navigation, back and forward, the
previous route as referrer, an external referrer on the first pageview, the tag rendered with the
release's integrity value, both kinds of custom event, Global Privacy Control and a tampered
tracker refused (`tests/e2e/specs`, `examples/react-router-ssr/test`).

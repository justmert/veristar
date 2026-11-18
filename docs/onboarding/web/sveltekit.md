---
title: "SvelteKit"
description: "Add the tracker to a SvelteKit app."
---

For SvelteKit apps. Tested by the example [`examples/sveltekit`](https://github.com/justmert/veristar/tree/main/examples/sveltekit)
(SvelteKit 3.1.1, Svelte 5.57.3, `@sveltejs/adapter-node` 6.0.2, Vite 8), which runs in the
browser test matrix.

## Where the tag goes

Put the tag in `src/app.html`, before `%sveltekit.head%`:

{/* include examples/sveltekit/src/app.html region=head */}
```html
<!-- paste the tag from the console in place of the next line, before %sveltekit.head% -->
<!-- tracker -->
%sveltekit.head%
```

The example replaces the comment with the tag in a `handle` hook so that tests can set their own
site id at run time. In your app the tag from the console stands there:

{/* snippet version=1.3.1 */}
```html
<script defer src="https://in.analytics.example/v/1.3.1/sdk.js" integrity="sha384-54fz21LNRgy6RLEdhwecMK8yavckH44JnSX9iSA3p2uiEtCEs6fyFt6LhuOL5kKw" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

`app.html` is the page template of every route, and SvelteKit copies markup outside its
`%sveltekit.*%` placeholders into each page as it is (the example relies on that: its hook finds
the comment unchanged in every rendered page). Keep the tag there rather than in `<svelte:head>`,
so it is part of every page exactly once and no component manages it.

## Route changes

The first load is rendered on the server and is one pageview. After that SvelteKit navigates on
the client with `history.pushState`, which the tracker watches, so every route change, back and
forward included, is one pageview.

## Custom events

`data-umami-event` attributes work in any component. For a call from code, use
`window.umami?.track()`:

{/* include examples/sveltekit/src/routes/+page.svelte region=track */}
```svelte
// the tracker may be blocked by the visitor, so the call is optional
function manualCall() {
  void window.umami?.track("manual-call", { source: "sveltekit" });
}
```

and declare the global in `src/app.d.ts` for TypeScript:

{/* include examples/sveltekit/src/app.d.ts */}
```ts
// window.umami as sdk.js installs it; optional because a visitor may block it
declare global {
  interface Window {
    umami?: {
      track: (name: string, data?: Record<string, string | number | boolean>) => Promise<void>;
    };
  }
}

export {};
```

## Content security policy

If you use SvelteKit's `kit.csp` option, add the gateway to `script-src` and `connect-src` as
[content-security-policy.md](content-security-policy.md) describes. The example does not set
`kit.csp`.

## What the browser tests check

One pageview for the server-rendered load and one per client-side route change, back and forward,
the previous route as referrer, an external referrer on the first pageview, both kinds of custom
event, Global Privacy Control and a tampered tracker refused (`tests/e2e/specs`).

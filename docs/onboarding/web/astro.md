---
title: "Astro"
description: "Add the tracker to an Astro site, static or server rendered."
---

For Astro sites, static or server rendered. Tested by the example
[`examples/astro-ssr`](https://github.com/justmert/veristar/tree/main/examples/astro-ssr) (Astro 7.4.12 with `@astrojs/node` in
standalone mode), which runs in the browser test matrix.

## Where the tag goes

Put the tag in the `<head>` of the layout every page uses, for example `src/layouts/Base.astro`.
The example renders the tag from the environment so that tests can use their own site id:

{/* include examples/astro-ssr/src/layouts/Base.astro region=head */}
```astro
<Fragment set:html={trackerTag()} />
```

In your layout, write the tag from the console as it is:

{/* snippet version=1.3.1 */}
```html
<script defer src="https://in.analytics.example/v/1.3.1/sdk.js" integrity="sha384-54fz21LNRgy6RLEdhwecMK8yavckH44JnSX9iSA3p2uiEtCEs6fyFt6LhuOL5kKw" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

Astro bundles and rewrites a `<script>` that has no attributes other than `src`, and leaves a
script with any other attribute exactly as written. The tag has several (`defer`, `integrity`,
`crossorigin`, `data-website-id`), so it reaches the browser unchanged. The example emits the tag
through `set:html`, and its test checks the rendered tag. Keep the tag in the layout's markup, not
in a component script.

Each full page load is one pageview.

## Custom events

`data-umami-event` attributes work in any template. For a call from page code, use a script with
`is:inline`, so Astro does not bundle it and it can read `window.umami`:

{/* include examples/astro-ssr/src/pages/index.astro region=track */}
```astro
<script is:inline>
  // umami.track() from page code; the tracker may be blocked, so check first
  document.getElementById("manual").addEventListener("click", () => {
    if (window.umami) window.umami.track("manual-call", { source: "astro-ssr" });
  });
</script>
```

## What the browser tests check

One pageview per server-rendered page load, the page title from the layout's `<title>` (the
matrix runs the current tracker release with `data-send-title="true"` on this site), both kinds
of custom event, an external referrer on the first pageview, Global Privacy Control and a tampered
tracker refused (`tests/e2e/specs`).

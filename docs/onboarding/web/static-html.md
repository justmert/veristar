---
title: "Static HTML"
description: "Add the tracker to hand-written pages and sites built by a static site generator."
---

For hand-written pages and for sites built by a static site generator. Tested by the example site
[`examples/static-html`](https://github.com/justmert/veristar/tree/main/examples/static-html), which runs in the browser test matrix.

## Where the tag goes

Paste the tag from the console into the `<head>` of every page. In the example, the build replaces
the comment `<!-- tracker -->` with the tag (it does so only so that tests can use their own site
id); on your site the tag itself stands there:

{/* include examples/static-html/src/index.html region=head */}
```html
<!-- paste the tag from the console in place of the next line, on every page -->
<!-- tracker -->
```

With the example values from the [quickstart](quickstart.md), the line becomes:

{/* snippet version=1.1.0 */}
```html
<script defer src="https://in.analytics.example/v/1.1.0/sdk.js" integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

If your static site generator has a head template or base layout that every page uses, put the
tag there, outside any helper that escapes HTML, and check one generated page to see that it
came out unchanged.

Pages without the tag are not counted. Each full page load is one pageview. A jump to an anchor on
the same page (`#details`) is not a pageview.

## Counting clicks

Any element with a `data-umami-event` attribute sends an event of that name when it is clicked;
each `data-umami-event-<key>` attribute adds one data entry:

{/* include examples/static-html/src/index.html region=event */}
```html
<button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
  Sign up
</button>
```

From your own script, call `umami.track()`. Keep page code in a file of its own if your site has a
content security policy:

{/* include examples/static-html/src/page.js region=track */}
```js
// umami.track() from page code; the tracker may be blocked, so check first
document.getElementById("manual").addEventListener("click", () => {
  window.umami?.track("manual-call", { source: "static-html" });
});
```

[custom-events.md](custom-events.md) lists the limits for names and data.

## What the browser tests check

In Chromium, Firefox and WebKit: one pageview per full load, no pageview for an anchor jump, the
page title and a same-site referrer as a path, both kinds of custom event, no request at all with
Global Privacy Control, the tracker refused when its file was tampered with, and every page
served with the content security policy from [content-security-policy.md](content-security-policy.md)
(`tests/e2e/specs`).

---
title: "Consent: starting the tracker later"
sidebarTitle: "Consent"
description: "Load the tracker without measuring, and start it once the visitor agrees."
---

Some sites must ask visitors before measuring them; whether yours must depends on the countries it
serves ([`docs/compliance/jurisdictions.md`](../../compliance/jurisdictions.md)). For those sites the
tracker can load without doing anything and start only when your consent banner says so. Tested by
the page [`consent.html`](https://github.com/justmert/veristar/blob/main/examples/static-html/src/consent.html) of the static example,
which runs in the browser test matrix.

## How it works

Add `data-auto-track="false"` to the tag from the console:

{/* snippet version=1.3.1 auto-track-off */}
```html
<script defer src="https://in.analytics.example/v/1.3.1/sdk.js" integrity="sha384-54fz21LNRgy6RLEdhwecMK8yavckH44JnSX9iSA3p2uiEtCEs6fyFt6LhuOL5kKw" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c" data-auto-track="false"></script>
```

With it, `sdk.js` loads and installs `window.umami`, but sends nothing, fetches no challenge and
reads no browser storage. When the visitor agrees, your code calls `umami.start()`. That sends the
pageview of the current page and from then on counts route changes and `data-umami-event` clicks,
exactly as the tracker does without the attribute. Calling `start()` again does nothing. From
tracker 1.3.2, the current page is the one the visitor is on when they agree, also after
single-page navigation while the banner was open, and the pageview keeps the visit's original
referrer; 1.3.1 and earlier count the page the tracker loaded on.

The tracker does not remember the choice; your site does, as it does for any other consent. On
the next page load, call `umami.start()` again as soon as you know the visitor agreed earlier.

## Example

The example page loads its own consent code after the tag, so it runs after `sdk.js`:

{/* include examples/static-html/src/consent.html region=head */}
```html
<!-- the tag from the console with data-auto-track="false" added -->
<!-- tracker:consent -->
<!-- the site's own consent code, after the tag so it runs after sdk.js -->
<script defer src="/consent.js"></script>
```

and keeps the choice in its own storage key:

{/* include examples/static-html/src/consent.js region=consent */}
```js
// the site's own consent banner. the tracker tag carries
// data-auto-track="false", so sdk.js loads but sends nothing until start().
// start() sends the pageview of the current page and from then on counts
// route changes and data-umami-event clicks, exactly as without the attribute.
// the choice is kept by the site, not by the tracker, which stores nothing.
const KEY = "example-analytics-consent";

function startAnalytics() {
  // the tracker may be blocked by the visitor, so the call is optional
  window.umami?.start();
}

function choose(granted) {
  localStorage.setItem(KEY, granted ? "granted" : "denied");
  document.getElementById("consent-banner").hidden = true;
  if (granted) startAnalytics();
}

document.getElementById("consent-accept").addEventListener("click", () => choose(true));
document.getElementById("consent-decline").addEventListener("click", () => choose(false));

// a visitor who agreed on an earlier visit is counted from the first page
if (localStorage.getItem(KEY) === "granted") {
  document.getElementById("consent-banner").hidden = true;
  startAnalytics();
}
```

If you use a consent management platform, call `window.umami?.start()` from its callback for the
analytics purpose instead. In a framework app, call it wherever your banner component learns the
choice; the tag stays where the framework guide puts it.

`umami.track()` before `start()` does nothing either: no event, no challenge and no storage read,
so a call that runs before the banner is answered measures no one. If your site sends only chosen
events and no automatic pageviews, call `umami.start({ automatic: false })` after consent instead
of `umami.start()`; from then on `umami.track()` sends.

This holds from tracker 1.3.1, the version in the tag above. Releases 1.3.0 and earlier send a
`umami.track()` call before `start()` at once, so on those releases call `umami.track()` only after
consent, or [upgrade](upgrading.md) to 1.3.1.

## An opt-out control

Where measurement runs without prior consent, visitors must still be able to object. The tracker
checks the opt-out flag `umami.disabled` in the site's local storage before every event, so a
button in your privacy notice only has to set it:

{/* include examples/static-html/src/page.js region=opt-out */}
```js
// an opt-out control for the site's privacy notice. the tracker checks this
// flag before every send, in this browser, from the next event on.
document.getElementById("opt-out").addEventListener("click", () => {
  localStorage.setItem("umami.disabled", "1");
  document.getElementById("opt-out").textContent = "Analytics turned off for this browser";
});
```

The flag holds for this browser and this site until the visitor clears the site's storage.

## Signals the tracker honours on its own

- **Global Privacy Control.** A browser that sends it is not measured at all, with or without
  consent, and the gateway drops any event that carries `Sec-GPC: 1`.
- **The opt-out flag** above.
- **Do Not Track**, only when the tag has `data-do-not-track="true"`.

## What the browser tests check

Before consent: `sdk.js` loads, and no challenge and no event is sent. After **Accept**: one
pageview of the consent page; after a reload, the stored choice starts the tracker again without a
click. After **Decline** and a reload: nothing is sent. After the opt-out button: no further event,
also after a reload. All in Chromium, Firefox and WebKit (`tests/e2e/specs/onboarding.spec.ts`).

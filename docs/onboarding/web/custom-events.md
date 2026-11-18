---
title: "Custom events"
description: "Send named events of your own, such as a click on a sign-up button, next to the pageviews."
---

Pageviews are counted without any code beyond the script tag. On top of them you can send your own
named events, for example a click on a sign-up button. They show up in the **Custom events** panel of
your project's **Web** page in the console, next to the pageviews. Every example site in the browser test matrix sends
both kinds of custom event below (`tests/e2e/specs/events.spec.ts`), and `tests/webpath` follows
them into Umami.

For wallet connect events from the Stellar Wallets Kit, see [wallets-kit.md](wallets-kit.md).

## Sending a custom event

Two ways, both from Umami's documented integration, so existing Umami instructions work as they are.

**From HTML**, with no JavaScript. A click on an element with `data-umami-event` sends an event of
that name. Every `data-umami-event-<key>` attribute on the same element becomes one data entry:

{/* include examples/static-html/src/index.html region=event */}
```html
<button type="button" id="signup" data-umami-event="signup-click" data-umami-event-plan="free">
  Sign up
</button>
```

This sends `signup-click` with the data `{ "plan": "free" }`. Attribute values are always strings.
A same-tab link with the attribute is not lost: the event goes out as the page is left. From
tracker 1.3.2, a link that a client-side router handles stays a client-side navigation, a plain link
waits for its event at most one second, and download links and links into another window are left
to the browser.

**From JavaScript**, with `veristar.track(name, data)`. The tracker installs one object under two
names, `window.veristar` and `window.umami`, so `umami.track(name, data)` is the same call and stays
supported. `window.veristar` is there from tracker 1.4.0; the example below uses `window.umami`,
which works on every release:

{/* include examples/static-html/src/page.js region=track */}
```js
// umami.track() from page code; the tracker may be blocked, so check first
document.getElementById("manual").addEventListener("click", () => {
  window.umami?.track("manual-call", { source: "static-html" });
});
```

Data values can be strings, numbers or booleans, for example
`veristar.track("plan-chosen", { plan: "pro", seats: 3, annual: true })`. `veristar.track()` returns a
promise. The visitor can block the tracker, so call it as `window.veristar?.track(...)` and never let
your own code depend on it. The framework guides show the same call in React, Vue, Svelte and
Next.js components.

If you installed the [npm package](npm.md) instead of the script tag, `init({ siteId, host })`
installs the same `window.veristar` and `window.umami`, so both ways above work unchanged.

## Limits

The tracker checks every event before it sends anything, and the gateway checks again.

| Item | Limit | What happens above it |
|---|---|---|
| Event name | up to 50 characters, no control characters | longer names are cut to 50 characters; control characters make `track()` reject. An empty name sends a pageview, as in Umami |
| Data shape | a flat object: one level of `key: value` | a nested object, an array or `null` makes `track()` reject |
| Number of keys | at most 16 | a 17th key makes `track()` reject |
| Key | 1 to 50 characters, no control characters | `track()` rejects |
| Value | a string, a finite number or a boolean | anything else (`null`, `undefined`, `NaN`, objects, arrays) makes `track()` reject |
| String value | up to 500 characters, no NUL character | longer strings are cut to 500 characters; a NUL character makes `track()` reject |
| Whole event | 8192 bytes on the wire | `track()` rejects |

"Rejects" means the promise returned by `veristar.track()` is rejected with a `TypeError` that names
the problem, for example `umami: event data has more than 16 keys`, and **nothing is sent**. The
tracker does not drop the extra keys for you, because silently losing data would hide the mistake;
the error shows up in your browser console instead.

A client that skips the tracker and posts such an event to the gateway directly gets `400` and the
event is not stored.

Lengths are counted in UTF-16 code units, the way JavaScript counts `string.length`.

## What is removed before an event is stored

The gateway redacts personal and secret values in the event name, every data key and every string
data value (and in the page URL, referrer and title) before the event is queued. It replaces:

| Found | Stored as |
|---|---|
| a Stellar secret seed (`S...`) | `{redacted-secret}` |
| an account address (`G...`) | `{account}` |
| a muxed account (`M...`) | `{muxed}` |
| a contract (`C...`) and a muxed contract (`W...`) | `{contract}`, `{muxed_contract}` |
| a liquidity pool, claimable balance, pre-auth tx, hash-x or signed payload | `{pool}`, `{balance}`, `{preauth}`, `{hashx}`, `{signed_payload}` |
| an email address | `{email}` |
| a federation address (`name*domain`) | `{federation}` |

Stellar keys are matched by their checksum, also when lower-cased or glued to other text. So
`umami.track("seed-check", { note: "key SB..." })` is stored with the data
`{ "note": "key {redacted-secret}" }`. Redaction is a safety net, not a licence: never put a secret
seed, an address or any other personal value into event data in the first place. If two keys of one
event redact to the same text, only one of them is kept.

Numbers and booleans are stored unchanged.

## Where to see them

Open **Web** in your project in the console. The **Custom events** panel lists every custom and
wallet event by name with how many times it was sent in the range. The event data you attach
(properties and their values) is available through the project's AI assistant connection
([../mcp.md](../mcp.md)), whose tools read it for your sites.

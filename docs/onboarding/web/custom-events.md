# Custom events and wallet events

Pageviews are counted without any code beyond the script tag. This page covers what you can add on
top: your own named events, and wallet events from the Stellar Wallets Kit. Both show up in your
site's Umami dashboard on the **Events** page, next to the pageviews.

## Sending a custom event

Two ways, both from Umami's documented integration, so existing Umami instructions work as they are.

**From HTML**, with no JavaScript. A click on an element with `data-umami-event` sends an event of
that name. Every `data-umami-event-<key>` attribute on the same element becomes one data entry:

```html
<button data-umami-event="signup-click" data-umami-event-plan="free">Sign up</button>
```

This sends `signup-click` with the data `{ "plan": "free" }`. Attribute values are always strings.
A same-tab link with the attribute waits for the event before it navigates, so the click is not lost.

**From JavaScript**, with `umami.track(name, data)`:

```js
umami.track("manual-call", { source: "pricing-page", seats: 3, annual: true });
```

`umami.track()` returns a promise. The visitor may block the tracker, so call it as
`window.umami?.track(...)` and never let your own code depend on it.

If you installed the npm package instead of the script tag, `init({ siteId, host })` installs the
same `window.umami`, so both ways above work unchanged.

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

"Rejects" means the promise returned by `umami.track()` is rejected with a `TypeError` that names
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

## Wallet events from the Stellar Wallets Kit

If your dapp uses [`@creit.tech/stellar-wallets-kit`](https://github.com/Creit-Tech/Stellar-Wallets-Kit)
(tested with 2.7.1 and 2.8.0), the tracker can count wallet activity for you:

| Event | Sent when | Data |
|---|---|---|
| `wallet_selected` | the visitor picks a wallet in the kit | `wallet`: the kit's wallet id, for example `xbull` |
| `wallet_connected` | the kit goes from no address to an address | `network`: `public`, `testnet`, `futurenet` or `other` (npm package only, see below) |
| `wallet_disconnected` | the visitor disconnects through the kit | none |

**The account address is never sent, hashed or stored.** It is only used to notice that a
connection happened. A visitor who comes back with the wallet still connected (the kit restores
the session from browser storage) is not counted again, and neither is a network switch or a
second address request while connected.

Wallet capture is off until you turn it on, with one line. It is not automatic on purpose: it means
reading the kit's own browser storage or events, and doing that on every site by default would be
access to stored information that a site without a consent banner should not do without deciding
to.

### With the npm package

Call `attachWalletsKit` once, after `StellarWalletsKit.init()`:

```ts
import { StellarWalletsKit, Networks } from "@creit.tech/stellar-wallets-kit";
import { defaultModules } from "@creit.tech/stellar-wallets-kit/modules/utils";
import { init } from "tracker";
import { attachWalletsKit } from "tracker/wallets-kit";

init({ siteId: "<your site id>", host: "https://in.<BASE_DOMAIN>" });
StellarWalletsKit.init({ modules: defaultModules(), network: Networks.PUBLIC });

const detach = attachWalletsKit(StellarWalletsKit);
// later, to stop capturing: detach();
```

This subscribes to the kit's own events, so `wallet_connected` carries the network. Attaching the
same kit twice throws, because every event would be counted twice. Calling it before `init()` of
the tracker throws too.

### With the script tag

Add `data-wallets-kit="true"` to the tag from the console:

```html
<script defer src="https://in.<BASE_DOMAIN>/v/1.1.0/sdk.js" integrity="sha384-..."
  crossorigin="anonymous" data-website-id="<your site id>" data-wallets-kit="true"></script>
```

The kit sets no browser global and fires no DOM events, so a separately loaded script cannot see
its events. Instead `sdk.js` checks the kit's two storage keys twice a second: whether
`@StellarWalletsKit/activeAddress` exists (by its name only; its value is never read) and which
wallet id `@StellarWalletsKit/selectedModuleId` holds. The kit keeps the network in memory only,
so in this mode `wallet_connected` has no `network`. Use the npm package if you need it.

Notes on the script-tag mode:

- It needs tracker version **1.1.0** or newer. Version 1.0.0 ignores the attribute.
- With `data-auto-track="false"` (consent-gated sites), storage is not read until you call
  `umami.start()`. With Global Privacy Control on, nothing is read or sent.
- A change made in another tab of your site is left to that tab, so it is not counted twice.
- A connect and disconnect within half a second can be missed.

## Where to see them

Open your site from the console. In Umami, the **Events** page of the website lists every custom
and wallet event by name. Members of your project see it read-only.

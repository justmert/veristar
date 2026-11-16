---
title: "Wallet events from the Stellar Wallets Kit"
sidebarTitle: "Wallets Kit"
description: "Count wallet connections and other wallet activity from the Stellar Wallets Kit."
---

If your dapp uses [`@creit.tech/stellar-wallets-kit`](https://github.com/Creit-Tech/Stellar-Wallets-Kit)
(tested with 2.7.1 and 2.8.0), the tracker can count wallet activity for you. Tested by the example
[`examples/wallets-kit-spa`](https://github.com/justmert/veristar/tree/main/examples/wallets-kit-spa), which `TestWalletsKit` in
`tests/webpath` runs in both modes below through the real gateway into Umami, and by the tracker's
own tests against both kit versions.

| Event | Sent when | Data |
|---|---|---|
| `wallet_selected` | the visitor picks a wallet in the kit | `wallet`: the kit's wallet id, for example `xbull` |
| `wallet_connected` | the kit goes from no address to an address | `network`: `public`, `testnet`, `futurenet` or `other` (npm package only, see below) |
| `wallet_disconnected` | the visitor disconnects through the kit | none |

**The account address is never sent, hashed or stored.** It is only used to notice that a
connection happened. A visitor who comes back with the wallet still connected (the kit restores
the session from browser storage) is not counted again, and neither is a network switch or a
second address request while connected.

## Why it is opt-in

Wallet capture is off until you turn it on, with one line. It is not automatic on purpose: it
means reading the kit's own browser storage or subscribing to its events. Doing that on every site
by default would make the tracker read another library's stored information on sites that never
asked for it, and sites that run without a consent banner rely on the tracker reading nothing but
its own opt-out flag. So each dapp decides, in its own code or tag, that wallet events are part of
its measurement.

## With the script tag

Add `data-wallets-kit="true"` to the tag from the console:

{/* snippet version=1.1.0 wallets-kit */}
```html
<script defer src="https://in.analytics.example/v/1.1.0/sdk.js" integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c" data-wallets-kit="true"></script>
```

The dapp itself only sets up the kit, as it would anyway:

{/* include examples/wallets-kit-spa/src/script-page.tsx */}
```tsx
// script tag mode: sdk.js is on the page with data-wallets-kit="true" and
// watches the kit's storage keys itself, so the dapp only sets up the kit
import { render } from "./app.tsx";
import { initKit } from "./wallets.ts";

await initKit();
render("script");
```

The kit sets no browser global and fires no DOM events, so a separately loaded script cannot see
its events. Instead `sdk.js` checks the kit's two storage keys twice a second, and whenever
another tab of your site changes storage. To find them it lists the names of your site's
`localStorage` keys (names only). It then reads which wallet id
`@StellarWalletsKit/selectedModuleId` holds and checks whether `@StellarWalletsKit/activeAddress`
exists; the address value, and the value of every other key, is never read. The kit keeps the network in memory only,
so in this mode `wallet_connected` has no `network`. Use the npm package if you need it.

Notes on the script-tag mode:

- It needs tracker version **1.1.0** or newer. Version 1.0.0 ignores the attribute.
- With `data-auto-track="false"` ([consent.md](consent.md)), storage is not read until you call
  `umami.start()`. Without it, a visitor with Global Privacy Control or the opt-out flag set has
  nothing read beyond the opt-out flag and nothing sent. If you call `umami.start()` for such a
  visitor, the kit's keys are watched from then on, but no event is sent; call `start()` only
  after consent.
- A change made in another tab of your site is left to that tab, so it is not counted twice.
- A connect and disconnect within half a second can be missed.

## With the npm package

Call `attachWalletsKit` once, after the tracker's `init()` and the kit's `init()`:

{/* include examples/wallets-kit-spa/src/module-page.tsx region=attach */}
```tsx
// after the kit's init, so the restored session is the starting point
attachWalletsKit(await initKit());
```

where `initKit` is the dapp's usual kit setup:

{/* include examples/wallets-kit-spa/src/wallets.ts region=kit */}
```ts
// the example dapp runs on testnet
export async function initKit(): Promise<typeof StellarWalletsKit> {
  StellarWalletsKit.init({ modules: await walletModules(), network: Networks.TESTNET });
  return StellarWalletsKit;
}
```

`attachWalletsKit` comes from the package's `wallets-kit` entry (`tracker/wallets-kit` in the
example) and returns a function that stops the capture. It subscribes to the kit's own events, so
`wallet_connected` carries the network. Attaching the same kit twice throws, because every event
would be counted twice. Calling it before the tracker's `init()` throws too. [The npm package](npm.md) explains how to
build and install the package.

## Where to see them

The **Custom events** panel of your project's **Web** page lists `wallet_selected`,
`wallet_connected` and `wallet_disconnected` next to your [custom events](custom-events.md). The
wallet id and network are stored as event data, which the AI assistant tools read
([../mcp.md](../mcp.md)).

## What the tests check

`TestWalletsKit` connects and disconnects a test wallet module in a real browser, in both modes,
and checks that the three events reach Umami with the expected data, that the address appears in
no request body, queue message or database row, and that `{redacted-secret}` replaces a secret seed
sent as event data. The tracker's unit tests run the capture against kit versions 2.7.1 and 2.8.0.

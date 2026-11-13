---
title: "Browser proof"
description: "The proof of work every page load solves, and what it does and does not prove."
---

Every page load that runs `sdk.js` fetches a challenge from `GET /v1/c`, solves a small SHA-256
proof of work with WebCrypto and attaches the result to every event of that page load. The exact
bytes are in [wire-v1.md](wire-v1.md). This page says what the proof is worth and what it costs.

## What it shows and what it does not

- A request without a valid proof did not go through the challenge flow. Naive server-side posts
  (a plain HTTP client posting a JSON body) are rejected.
- It does **not** prove that a browser ran the page. A scripted client can fetch a challenge, solve
  it in a few milliseconds and forge `Origin` and `Sec-Fetch-*` headers. The proof raises the cost
  of forged traffic and catches naive posts. It cannot stop a determined actor.
- Replay protection is an in-memory limit on each edge node. A captured proof can be replayed up to
  the per-proof limit on each of the three nodes, at most three times the limit in total.
- The challenge is held in page memory only: no cookie, no `localStorage`, no `sessionStorage`. A
  new page load gets a new challenge.
- Difficulty (`bits`) and time (`iat`) come from the server, so difficulty can change without a
  tracker release, and a wrong client clock does not matter. The tracker refuses challenges above
  24 bits, so a misconfiguration cannot freeze visitors' browsers.

The plan's `docs/security/bot-and-inflation.md` (this stage) covers the whole bot and inflation
strategy.

## Chosen difficulty

**`PROOF_BITS = 12`** is the gateway's starting value. It is the highest tested value whose median
solve time stays under 150 ms at a 6x CPU slowdown in all three engines. 14 bits exceeds 150 ms in
every engine at 6x.

## Measurements (2026-11-12)

`packages/tracker/bench/proof-cost.mjs`, raw numbers in
`packages/tracker/bench/results/2026-11-12.json`. Intel Core Ultra 5 225, headless Playwright
1.64.1: Chromium 156.0.8078.4, Firefox 157.0, WebKit 27.2. 101 solves per cell, each against a fresh
random salt, after a warm-up. Batch size 64 (see below). Times in milliseconds.

Playwright can throttle the CPU only in Chromium (CDP `Emulation.setCPUThrottlingRate`). To slow all
three engines the same way, each engine ran inside a transient systemd scope with a CPU quota
(`CPUQuota=25%` for 4x, `17%` for 6x, quota period 10 ms). Chromium was measured both ways; the two
methods agree within noise. The quota caps all browser processes together, so for Firefox, which
spreads the work over more processes, it is harsher than 6x (its hash rate fell 12x).

Solve time grows with `2^bits` and is geometrically distributed, so p90 is about 3x the median.

| Engine | Throttle | bits 10 median / p90 | bits 12 median / p90 | bits 14 median / p90 |
|---|---|---|---|---|
| Chromium | none | 1.3 / 5.8 | 5.5 / 25.8 | 27.6 / 95.1 |
| Chromium | 4x CDP | 8.2 / 21.7 | 24.6 / 71.2 | 111 / 336 |
| Chromium | 6x CDP | 11.6 / 34.6 | **37.4** / 144 | 187 / 605 |
| Chromium | 4x quota | 8.8 / 20.9 | 29.6 / 81.7 | 121 / 452 |
| Chromium | 6x quota | 10.7 / 39.9 | **50.5** / 180 | 190 / 572 |
| Firefox | none | 3 / 9 | 9 / 26 | 45 / 160 |
| Firefox | 4x quota | 20 / 80 | 50 / 208 | 260 / 790 |
| Firefox | 6x quota | 41 / 159 | **109** / 400 | 471 / 1521 |
| WebKit | none | 2 / 5 | 6 / 17 | 19 / 59 |
| WebKit | 4x quota | 9 / 41 | 40 / 169 | 138 / 501 |
| WebKit | 6x quota | 11 / 49 | **50** / 168 | 229 / 750 |

Firefox and WebKit report `performance.now()` at 1 ms resolution.

**Batch size.** Each WebCrypto digest is an asynchronous call, so the solver issues digests in
batches and awaits them together. Hash rate in hashes per ms at 12 bits:

| Engine, throttle | batch 1 | 16 | 64 | 256 |
|---|---|---|---|---|
| Chromium, none | 465 | 495 | 489 | 493 |
| Chromium, 6x quota | 62 | 63 | 65 | 62 |
| Firefox, none | 99 | 268 | 283 | 278 |
| Firefox, 6x quota | 11 | 28 | 26 | 24 |
| WebKit, none | 82 | 515 | 620 | 655 |
| WebKit, 6x quota | 6 | 43 | 49 | 52 |

One digest at a time is up to 8x slower in WebKit and 3x in Firefox. 64 is at or within noise of
the best everywhere, so `SOLVE_BATCH = 64`.

## Request behaviour in each engine

`packages/tracker/bench/cors-probe.mjs`, raw output in `bench/results/cors-2026-11-12.json`. Page
on `http://127.0.0.1`, gateway on `http://localhost` (two different sites), the tracker's exact
`fetch` options:

| Engine | Preflight `OPTIONS` | `GET /v1/c` | `POST /v1/e` | Cookie sent |
|---|---|---|---|---|
| Chromium 156 | none | `Sec-Fetch-Site: cross-site`, `Mode: cors`, `Dest: empty` | same, `Content-Type: text/plain;charset=UTF-8` | no |
| Firefox 157 | none | same | same | no |
| WebKit 27.2 | none | same | same | no |

All three send `Origin` on both requests. The gateway's fetch-metadata check (this stage) can expect
`cross-site` (or `same-site` when the tenant site shares the base domain), `cors` and `empty`.

## Filter lists

`tests/adblock/` downloads EasyList, EasyPrivacy, uBlock filters, uBlock privacy, uBlock badware,
AdGuard Base, AdGuard Tracking Protection and Peter Lowe's list on every run and checks the three
public urls with `@ghostery/adblocker` as `script`, `xmlhttprequest` and `ping` requests from a
third-party page. On 2026-11-12 no list matched any of them as `script` or `xmlhttprequest`. The
only `ping` match is EasyPrivacy's generic `$ping,3p`, which blocks every third-party beacon
whatever its url; the tracker never sends `ping` requests (it uses `fetch`, never `sendBeacon`),
and the test checks that too.

## Observed request headers

Recorded on 2026-11-12 by `tests/e2e/specs/headers.spec.ts` with Playwright 1.64.1, from a tenant page on `https://static.sa-tenant.example` to the gateway on `https://in.sa-test.example` (two different registrable domains, as in production). The recorder is the test-only gateway fixture in `tests/e2e/fixture/`. No engine sent an `OPTIONS` request in any test of the matrix, and none sent a `Cookie` header.

| Request | Engine | `Sec-Fetch-Site` | `Sec-Fetch-Mode` | `Sec-Fetch-Dest` | `Origin` | `Content-Type` |
|---|---|---|---|---|---|---|
| `GET /v/1.0.0/sdk.js` | Chromium 156.0.8078.4 | `cross-site` | `cors` | `script` | site origin | none |
| | Firefox 157.0 | `cross-site` | `cors` | `script` | site origin | none |
| | WebKit 27.2 | `cross-site` | `cors` | `script` | site origin | none |
| `GET /v1/c` | Chromium 156.0.8078.4 | `cross-site` | `cors` | `empty` | site origin | none |
| | Firefox 157.0 | `cross-site` | `cors` | `empty` | site origin | none |
| | WebKit 27.2 | `cross-site` | `cors` | `empty` | site origin | none |
| `POST /v1/e` | Chromium 156.0.8078.4 | `cross-site` | `cors` | `empty` | site origin | `text/plain;charset=UTF-8` |
| | Firefox 157.0 | `cross-site` | `cors` | `empty` | site origin | `text/plain;charset=UTF-8` |
| | WebKit 27.2 | `cross-site` | `cors` | `empty` | site origin | `text/plain;charset=UTF-8` |

All three engines also sent `Accept: */*` and `Referer` set to the page URL (without fragment) on all three requests. With Global Privacy Control on (Firefox preference `privacy.globalprivacycontrol.enabled`), the tracker loaded but sent neither `/v1/c` nor `/v1/e`.

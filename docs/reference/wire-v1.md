---
title: "Wire format v1"
description: "How sdk.js talks to the gateway: endpoints, payloads and limits."
---

How `sdk.js` talks to the gateway at `in.<BASE_DOMAIN>`. The TypeScript source of truth is
`packages/tracker/src/wire.ts`; `internal/events/wire.go` mirrors it. A change to either needs the
same change in the other and in this page.

## Endpoints

| Request | Purpose |
|---|---|
| `GET /v/<version>/sdk.js` | the tracker, an immutable file listed in `packages/tracker/releases/manifest.json` |
| `GET /v1/c?w=<site id>` | a proof-of-work challenge for one page load |
| `POST /v1/e` | one event |

A request for `sdk.js` names no site. The gateway attributes it to a site from the page's origin in
the `Referer` header and counts it, and counts it again when it carries `Sec-GPC: 1`, for the
estimate of visits not measured because of privacy signals
([../compliance/privacy-strategy.md](../compliance/privacy-strategy.md), section 6).

Both `/v1/c` and `/v1/e` are CORS simple requests: the tracker sends no custom headers and uses
`credentials: 'omit'`, so the browser sends no preflight and no cookies. The gateway answers both
with `Access-Control-Allow-Origin` set to the request's `Origin`, whatever it is, and
`Vary: Origin`, so a refusal reaches the page as its status instead of a CORS error. Echoing the
origin grants nothing, because no credentials are allowed; whether the origin is registered for
the site is the gateway's own check, which answers 403 when it is not.

## Challenge

`GET /v1/c?w=<site id>` answers `200` with JSON:

```json
{ "w": "<site id>", "iat": 1794528000, "salt": "<32 hex>", "bits": 12, "tag": "<64 hex>" }
```

| Field | Type | Meaning |
|---|---|---|
| `w` | string | the site id from the query, echoed |
| `iat` | integer | server time in seconds when the challenge was issued |
| `salt` | string | 16 random bytes as 32 lowercase hex characters |
| `bits` | integer | required leading zero bits, the gateway setting `PROOF_BITS` |
| `tag` | string | `hex(HMAC-SHA256(proof_key, tagInput))`, 64 lowercase hex characters |

`tagInput` is the UTF-8 string `w|iat|salt|bits`, with `iat` and `bits` in decimal and `salt` in the
hex form above. `proof_key` is the daily key in the `edge_secrets` bucket.

The tracker refuses a challenge whose `w` differs from its site id, or with any field outside the
types above, or with `bits` above 24 (`MAX_PROOF_BITS`). It then sends nothing and asks again on the
next event. The challenge is kept in memory only. The tracker fetches a new one after 5 hours of
local monotonic time, inside the gateway's 6-hour validity.

A single-page app keeps one page load for as long as the tab is open, while the gateway accepts
one proof for `PROOF_REPLAY_LIMIT` queued events per node (50). From release 1.2.0 the tracker
fetches a new challenge after 40 events (`PROOF_MAX_USES` in `wire.ts`), and when an event is
answered 403 with `rejected_replay`, `rejected_challenge_expired` or `rejected_challenge_tag`
it forgets the proof and sends that event once more with a new one. Releases 1.0.0 and 1.1.0
keep one proof per page load and lose the events of a long session after the 50th per node.

## Proof

The tracker searches `nonce = 0, 1, 2, ...` and takes the smallest nonce for which
`SHA-256(proofInput)` has at least `bits` leading zero bits, counted from the most significant bit
of the first byte. `proofInput` is the UTF-8 string `w|iat|salt|nonce`, all decimal except `salt`.
None of the parts can contain `|`.

Test vectors, with the proof key, tag, nonce and digest, are in
`packages/tracker/test/vectors/proof-v1.json`. The Go side and the e2e recorder build their
challenges from the same file.

## Event

`POST /v1/e`, `Content-Type: text/plain;charset=UTF-8`, body a JSON object of at most 8192 bytes,
sent with `fetch` and `keepalive: true`.

| Field | Type | Required | Limit | Meaning |
|---|---|---|---|---|
| `v` | integer | yes | | always `1` |
| `sv` | string | no | 20 | tracker release as `x.y.z`, sent from 1.2.0 on; the gateway answers 410 when it names a revoked release |
| `w` | string | yes | | site id |
| `t` | string | yes | | `pv` pageview, `ev` custom event or `nv` never-visible notice (1.3.0 on, see below) |
| `h` | string | yes | 100 | hostname of the page; the gateway replaces it with the host of the checked `Origin` header, so a page can report only its own host, then lower cases and redacts it like the referrer host (a wildcard origin accepts any subdomain label) |
| `u` | string | `pv` and `ev` | 500 | path and query of the page, plus a route fragment (see below) |
| `r` | string | `pv` and `ev` | 500 | referrer: full url for another origin, path for the same origin, empty if none; the gateway keeps only scheme and host (see Referrer below) |
| `ti` | string | no | 500 | document title; releases before 1.3.0 always send it, 1.3.0 only with `data-send-title="true"` or when the site's own `data-before-send` hook or `umami.track(props)` call sets it |
| `s` | string | no | 11 | screen as `WxH`; releases before 1.3.0 always send it, 1.3.0 never reads it and sends it only when the site's own `data-before-send` hook or `umami.track(props)` call sets it |
| `l` | string | no | 35 | `navigator.language`; releases before 1.3.0 always send it, 1.3.0 never reads it and sends it only when the site's own `data-before-send` hook or `umami.track(props)` call sets it |
| `fr` | boolean | no | | `true` when the page runs inside a frame, sent from 1.3.0 on every event of such a page; absent at top level |
| `n` | string | `ev` only | 50 | event name |
| `d` | object | optional, `ev` only | 16 keys | event data, see below |
| `ts` | integer | yes | | client clock in epoch ms, informational, never trusted |
| `p` | object | yes | | `{iat, salt, bits, tag, nonce}`: the challenge fields plus the integer nonce |

**Fragments.** A fragment can carry secrets, for example an OAuth access token after an implicit
grant redirect (`/callback#access_token=...`), so it is never sent by default. It is sent only when
it starts with `#/` (hash-based SPA routing) and `data-exclude-hash` is not set, and then without any
query part inside it: `/app#/users/7?tab=2` is sent as `/app#/users/7`. Every other fragment
(`#section`, `#access_token=...`, `#`) is dropped. The same rule applies to the url a
`data-before-send` hook or `umami.track(props)` supplies. Because pageviews compare urls after this
rule, a jump to an in-page anchor is not counted as a pageview, while a change of hash route is.

**Referrer.** The tracker sends what the browser gives it (`r` above), but the gateway keeps only
the scheme and host of a referrer from another origin before the event is queued:
`https://news.example/story/42?utm_campaign=x#top` is stored as `https://news.example`. The port,
user info, path, query and fragment are dropped, the host is lower cased and redacted like every
other field, and a same-origin path, a scheme-relative url, an opaque url (`mailto:`) or anything
that does not parse becomes empty. CNIL's self-assessment tool for consent-exempt audience
measurement lists a referrer limited to the host as its measure against importing outside data
(`docs/compliance/sources.md`, S2), and the referrers metric needs only the host
(`internal/gateway/sanitize.go`, `SanitizeReferrer`).

Limits are in UTF-16 code units, as JavaScript counts string length. The tracker cuts `h`, `u`,
`r`, `ti`, `s`, `l` and `n` to their limits (never splitting a surrogate pair), so automatic fields
never make an event invalid. Unknown fields are not sent.

**Event data** is a flat object: at most 16 keys, each 1 to 50 characters, each value a string (cut
to 500 characters, no nul character), a finite number or a boolean. Values Umami 3.5.0 cannot
store are refused as well (tracker from 1.2.0 and gateway; cases in
`packages/tracker/test/vectors/storable-v1.json`): a number of 1e15 or more in absolute value
(`event_data.number_value` is `numeric(19,4)`), a `revenue` of 1e14 or more when `currency` is
a non-blank string (Umami's own check), and a string in Umami's date format
(`YYYY-MM-DDTHH:MM:SS`, optional `.mmm`, `Z` or `+HH:MM`) dated year 0000 or 0001-01-01, which the
timestamp column refuses. An event name may not start with `=`, `+`, `-` or `@`, which Umami
refuses as spreadsheet formula triggers. The tracker refuses anything
else: `null`, nested objects, arrays, `undefined`, `NaN`, too many keys, too long keys, keys with
control characters, and a body over 8192 bytes. An event name with control characters is refused
the same way. In
that case `umami.track()` returns a rejected promise and nothing is sent, so the mistake shows in
the site's console instead of disappearing.

Example of an event from a release before 1.3.0:

```json
{"v":1,"w":"8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b","t":"ev","h":"site.example","u":"/pricing?plan=pro",
 "r":"https://ref.example/a","ti":"Pricing","s":"1920x1080","l":"en-US","n":"signup","d":{"plan":"pro"},
 "ts":1794528001234,"p":{"iat":1794528000,"salt":"00112233445566778899aabbccddeeff","bits":8,
 "tag":"<64 hex>","nonce":87}}
```

All events of one page load carry the same proof. What the proof shows and what it costs is in
[browser-proof.md](browser-proof.md); the order of the gateway's checks is in
[../security/bot-and-inflation.md](../security/bot-and-inflation.md), "Where the checks run".

## Pages a person can see (1.3.0)

Release 1.3.0 sends the automatic pageview only for a page a person can see, and reports two page
signals. The rules, all in `packages/tracker/src/index.ts`:

| Situation | What 1.3.0 sends |
|---|---|
| The page is prerendered (`document.prerendering`) | nothing, and no challenge request, until the `prerenderingchange` event; then the pageview |
| The page loads in a background tab (`document.visibilityState` is `hidden`) | the challenge only; the pageview when the page first becomes visible (`visibilitychange`) |
| The page is shown, hidden and shown again | one pageview: the same page |
| The page is restored from the back/forward cache (`pageshow` with `persisted`) | one more pageview, once the page is visible |
| A single-page app changes its route (`pushState`, `replaceState`, `popstate`) | one pageview per route, as before; routes changed while the tab is hidden count as one pageview of the route the person then sees |
| The page is left (`pagehide`) without ever having been visible | one `nv` notice, if the proof for it was already solved |
| The page runs inside a frame | `fr: true` on every event |

`umami.track()` is unchanged: it sends what the site asks for, visible or not.

**Never-visible notice.** `{"v":1,"sv":"1.3.0","w":"<site id>","t":"nv","h":"<host>","ts":...,"p":{...}}`,
plus `fr` when framed. It carries no `u`, `r`, `ti`, `s`, `l`, `n` or `d`; the gateway refuses a
notice with any of them (`page_fields_on_notice`). The notice passes every check an event passes,
including the origin and the proof, and is sent during `pagehide` with `keepalive`, so only a proof
the page already holds can be used: a page closed within moments of loading may send none, and the
count is a lower bound. The gateway answers 202 and never queues it; it counts the notice for the
site only when the per-visitor volume bound and the same-network check would have put an event of
that visitor in the primary band.

**What the gateway does with the signals.** For each pageview of a release that reports signals
(`sv` 1.3.0 or newer) that is queued in the primary band it counts `signal_views`, and
`signal_framed` when `fr` is true; a quarantined pageview counts neither. Each notice counted as
above adds `signal_never_visible`. The counts are per site and UTC day on
`EDGE_STATS` and are inputs of integrity rule version 2, where they can only lower a grade
([../security/bot-and-inflation.md](../security/bot-and-inflation.md), section "Page signals").

The wire format stays version 1: every field 1.3.0 adds is optional, and the gateway accepts the
bodies of every earlier release unchanged. A gateway older than 1.3.0 refuses `fr` and `nv` as
unknown, and requires `ti`, `s` and `l`, so it refuses every event of a default 1.3.0 tracker.
That is why the gateway ships before the console offers 1.3.0.

## Consent-gated start

With `data-auto-track="false"` the tracker loads but sends nothing and fetches no challenge. Once
the visitor has consented, the site calls `umami.start()`, which sends the pageview for the current
page and turns on SPA navigation tracking and `data-umami-event` click capture, exactly as automatic
tracking would have. A second call does nothing. From tracker 1.3.1, `umami.track()` before
`start()` does nothing either: it sends no event, fetches no challenge, reads no storage and does not
run a `track(fn)` callback. A site that wants to send only chosen events calls
`umami.start({ automatic: false })`, which starts `umami.track()` without the automatic pageview,
SPA navigation tracking and click capture. Releases 1.0.0 to 1.3.0 send a `umami.track()` call
before `start()` at once. Sites in jurisdictions that need opt-in use this; the
default, without the attribute, is to start at once.

## Differences from Umami's `/api/send`

Removed: `data-tag`, `identify`, distinct ids, client salts, web vitals, error capture, the
credentials option, recorder hooks and the `x-umami-cache` header.

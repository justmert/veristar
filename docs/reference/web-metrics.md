---
title: "Web metrics"
description: "How each of the seven web metrics is produced, and how visitors and visits are counted."
---

How each of the seven web metrics the RFP requires (page views, unique visitors, referrers,
session duration, bounce rate, country, device and browser) is produced from Umami's tables
and read routes, and the two rules that decide how visitors and visits are counted on this
platform. Umami facts below are read from tag `v3.5.0` (paths relative to its `src/`) and
confirmed by the end-to-end suite `tests/webpath` (`make webpath`, the design).

## Where the data comes from

Every event a browser sends is accepted by the gateway and replayed into Umami by the
replayer as one `POST /api/send`:

| Umami input | Set from | Effect |
|---|---|---|
| `payload.timestamp` | gateway acceptance time, unix seconds | `website_event.created_at`, and the session's first and last time. A backlog replays into its original minute. |
| `payload.salt` | visitor token `vt` | mixed into Umami's daily salt (`ENABLE_CLIENT_SALT=1`), so it is part of the session id |
| header `x-edge-client-ip` | the constant `192.0.2.1` | the same for every visitor; no address reaches Umami |
| header `cf-ipcountry` | country the gateway looked up in DB-IP | `session.country` |
| header `User-Agent` | the browser's user agent | Umami's bot check, `session.browser`, `os`, `device` |
| header `x-umami-cache` | Umami's last answer for this visitor, while the gap is at most 1800 s | keeps the visit id |
| `url`, `referrer`, `title`, `screen`, `language`, `name`, `data` | the wire event after redaction | `website_event` and `event_data` columns, `session.screen`, `session.language` |
| `referrer` | only `scheme://host` of a referrer from another origin; empty for a same-origin path or no referrer (`wire-v1.md`, Referrer) | `website_event.referrer_domain` is that host without `www.`; `referrer_path` is `/` and `referrer_query` empty for every event |

Umami's session id is `uuid(website, ip, userAgent, hash(dailySalt, vt), distinctId)` and its
visit id is `uuid(sessionId, hash(startOfHour(createdAt)))` unless a cache token carries an
earlier visit id (`lib/collection-session.ts`). With the constant address, a session is one
visitor token with one user agent on one website on one UTC day.

## The seven metrics

All read routes take `startAt` and `endAt` in unix milliseconds and count only events whose
`created_at` is in that range. "Pageview events" are rows with `event_type` 1; custom events
(`event_type` 2) never count as views and never make a visit.

| RFP metric | Umami field and route | Definition in Umami (`queries/sql/...`) |
|---|---|---|
| Page views | `pageviews` of `GET /api/websites/<id>/stats`; per bucket `GET /pageviews?unit=` | number of pageview events (`getWebsiteStats.ts`, `sum(c)`) |
| Unique visitors | `visitors` of `/stats` | `count(distinct session_id)` over pageview events. On this platform a session is one visitor per UTC day (see the daily rule below). |
| Visits (used by the two below) | `visits` of `/stats` | `count(distinct visit_id)` over visits that contain at least one pageview |
| Session duration | `totaltime` of `/stats`, seconds; the dashboard shows `totaltime / visits` as "Visit duration" | sum over visits of the time between the visit's first and last pageview. The time spent on the last page is not measured (no event marks leaving it). |
| Bounce rate | `bounces / visits` of `/stats`; the dashboard shows it as "Bounce rate" | a bounce is a visit with exactly one pageview and no custom event |
| Referrers | `GET /metrics?type=referrer` | `referrer_domain` of pageview events, excluding the site's own hostname and empty values; `y` is distinct sessions. The tracker sends same-origin referrers as a path and the gateway turns them into an empty referrer, so internal navigation never counts. The gateway keeps only the scheme and host of any other referrer, so Umami stores `/` as every referrer path and an empty referrer query; the metric lists hosts, which is all it ever listed. |
| Country | `GET /metrics?type=country` | `session.country`, ISO 3166-1 alpha-2 from the `cf-ipcountry` header the replayer sets from the gateway's DB-IP lookup; `y` is distinct sessions. Region and city stay null: no header carries them and the constant address has no GeoLite entry. |
| Device and browser | `GET /metrics?type=device`, `type=browser`, `type=os` | `session.device`, `browser`, `os` from Umami's parsing of the original user agent (`lib/detect.ts`); a desktop user agent with a screen up to 1920 px wide is `laptop`, wider is `desktop`; `y` is distinct sessions |

The metrics bar of the overview page shows Visitors, Visits, Views, Bounce rate and Visit
duration; the Sources, Location and Environment panels show referrers, countries, and
browsers, OS and devices (`docs/reference/umami-contract.md` item 11).

## Unique visitors are unique per UTC day

Two things change at 00:00 UTC, and either one alone gives a new visitor:

1. The gateway's `daily_secret` rotates and is destroyed, so the same browser gets a new
   visitor token `vt`.
2. Umami runs with `SALT_ROTATION=daily` and `TZ=UTC`, so its base salt changes with the
   event's day (`lib/crypto.ts` `getSalt`, `startOfDay` of `created_at`).

Consequences:

- A person who visits on Tuesday and Wednesday is two visitors. Visitors over a multi-day
  range equal the sum of the daily visitors. Measured: one visitor token on three days gives 1
  visitor per day and 3 over the three days (`TestWebPath/time_ranges`).
- A visit that runs across midnight splits into two visits of two visitors, because the
  session id changes (Umami treats a cache token of another session as drift).
- The same browser with a different user agent (an update, another browser) is another
  visitor the same day. Two browsers behind one address are two visitors; one browser that
  changes address is a new visitor too, because the address is part of `vt`.
- A restart of the whole NATS cluster during a day loses `edge_secrets` (memory storage), so
  every returning visitor of that day is counted again.

## The visit rule

A visit ends when the visitor is idle for more than 30 minutes, with one exception that comes
from Umami's own visit id.

- While the gap to the visitor's previous event on that website is at most 1800 s, the
  replayer sends Umami's last cache token, and the visit continues, also across UTC hours.
- When the gap is longer, the replayer sends no cache token. Umami then derives the visit id
  from the session and the UTC hour of the event. An event in a different UTC hour than the
  visit's start therefore starts a new visit, but an event in the same UTC hour joins the
  visit that started in that hour.

| Two events of one visitor | Visits | Why |
|---|---|---|
| 10:50 and 11:21 (31 min, different hours) | 2 | gap over 30 min, no cache token, different hour |
| 13:05 and 13:36 (31 min, same hour) | 1 | gap over 30 min, no cache token, but the same hour gives the same visit id |
| 15:50 and 16:15 (25 min, different hours) | 1 | gap at most 30 min, the cache token keeps the visit |

All three rows are measured by `TestWebPath/visit_rule`. The same-hour case is Umami's own
behaviour for events without a cache token, which is how Umami treats timestamped events
(`historical` skips its 30 minute expiry), so a visit on this platform can last longer than
30 minutes of idle time only inside the UTC hour in which it started. The gap is measured on
gateway acceptance times, so a backlog replayed after an outage forms the same visits as live
delivery.

## Bands

Each site has a primary website (id equals the site id) and a quarantine website. Events
from an address prefix go to the quarantine website from the event that brings the prefix to
`IPCLUSTER_THRESHOLD` distinct visitor tokens for the site within an hour
(`internal/gateway/ipcluster.go`). Every metric above is
computed per website, so quarantined traffic is counted separately and never mixed into the
primary numbers.

## What is not counted

- Bots: Umami's `isbot` check runs on the original user agent; a bot event answers
  `{"beep":"boop"}`, stores nothing and is counted as `bot` on `EDGE_STATS` and in
  `replayer_outcomes_total`.
- Events the gateway refused (`Sec-GPC: 1`, bad proof, wrong origin, rate limits); they are
  counted on `EDGE_STATS` by outcome.
- Visits that hold only custom events: they have no pageview, so Umami's `/stats` does not
  count them as visits, visitors or bounces.
- Events whose timestamp is not after a website reset: Umami clamps every read to the
  website's `resetAt` (`lib/request.ts`). The replayer sends whole acceptance seconds, so an
  event accepted in the same second as a reset, or a backlog accepted before it and replayed
  after it, is stored but never shown.

## Daily aggregates (this stage)

The operator views read `agg.web_daily` and `agg.web_daily_dim`, which the
jobs task `aggregate-web` (`internal/jobs/aggregateweb`) fills from Umami's
own read routes, as the admin, for each active site, both bands and each UTC
day: `/stats` gives one `web_daily` row, `/metrics` with `type` `referrer`,
`country`, `device`, `browser` and `os` gives the `web_daily_dim` rows.
A run reads today and the 3 days before it every hour; `jobs run
aggregate-web --from <day> --to <day>` reads any range again. Each site day
replaces its rows in one fenced transaction, so re-running changes nothing
and a backfill restores deleted rows.

The research items of the design were measured on 2026-11-12 against the
dev Umami (the pinned 3.5.0 image, events sent through `/api/send` the way
the replayer sends them, three UTC days, six visitors, a bot, a custom event,
own-host and `www.` referrers, an event at exactly 00:00:00) and are asserted
by `TestAggregateWeb` (`internal/jobs/aggregateweb`) on every run.

| Question | Answer | Source in Umami |
|---|---|---|
| What does `y` count? | For every type used here, `count(distinct session_id)` over page view events (`event_type` not 2 or 5) in the range with a non-empty value. A visit that holds only a custom event is not counted. On this platform a session is one visitor on one UTC day, so `y` is "visitors of that day whose page views carry the value". A visitor can carry two referrers in a day and then counts under both, so the values of `referrer` can add up to more than `visitors`. | `queries/sql/pageviews/getPageviewMetrics.ts` (referrer, domain), `queries/sql/sessions/getSessionMetrics.ts` (country, device, browser, os) |
| `referrer` or `domain`? | The same. Both read `website_event.referrer_domain` (`FILTER_COLUMNS` in `lib/constants.ts`) and drop empty values and the site's own host, so the two answers are identical row for row. The stored domain is already lowercased with `www.` removed (`https://www.google.com/...` is `google.com`, `GitHub.com` is `github.com`); a referrer on the site's own host, with or without `www.`, is stored empty and never listed. The task stores `referrer` only. | as above |
| Unknown values | A null or empty value (country without a `cf-ipcountry` header) is left out of `/metrics`; the visitor still counts in `/stats`. | `and <column> != ''` |
| Day boundaries | `startAt` and `endAt` are both included (`created_at between start and end`). With the next midnight as `endAt`, an event at exactly 00:00:00 counts on two days (measured: 8 page views instead of 7). The task reads a day as `[00:00:00.000, 23:59:59.999]` UTC. | `getWebsiteStats.ts`, `getPageviewMetrics.ts` |
| Visitors over a range | Equal to the sum of daily visitors. Measured over three days: page views 13 = 7 + 3 + 3, visitors 10 = 5 + 3 + 2, visits 10 = 5 + 3 + 2, bounces 7 = 3 + 3 + 1, total time 1560 s = 360 + 0 + 1200; every `/metrics` value of the range equals the sum of its daily values (for example `github.com` 4 = 2 + 2, `DE` 5 = 2 + 2 + 1). The same client salt on three days gave three sessions, because Umami's daily salt changes with the event's day even before the gateway's visitor token rotates. | `lib/crypto.ts` `getSalt`, `lib/collection-session.ts` |
| Paging | `/metrics` takes `limit` (default 500) and `offset`, and orders only by `y desc`, so values with equal counts have no stable order across pages. The task asks for one page of 1,000 values, doubles the limit while a page comes back full, and fails the site day above 64,000 values instead of paging. | `getPageviewMetrics.ts`, `getSessionMetrics.ts` |
| Unknown website | `/stats` and `/metrics` of a website id that does not exist answer 200 with zeros for the admin. The task therefore checks that both websites of a site exist before it reads, and reports a missing one as an error rather than storing a day without traffic. | `canViewWebsite` passes for an admin |
| `type=url` | Not a type in 3.5.0 (400); `path` and `fullPath` replaced it. The operator views do not use paths. | `lib/constants.ts` |

Consequences for the operator views (`docs/reference/control-plane-api.md`
section 8):

- Visitors, visits, bounces and total time over a range are sums of daily
  rows, which equal Umami's answer for the same range. A project's numbers
  are the sum of its sites; one person on two sites of a project is two
  visitors, as Umami counts per website.
- Bounce rate is `bounces / visits` and average visit duration
  `totaltime / visits`, the formulas of Umami's dashboard.
- A breakdown value over a range is the sum of its daily values, which equals
  Umami's `/metrics` for the range.
- Removed tenants are not read again; the purge step `agg` deletes their rows
  unless `removal_keep_aggregates` keeps them.

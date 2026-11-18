---
title: "What the numbers mean"
description: "Bands, daily-unique visitors and span-derived API data, and how they shape cross-project comparisons."
---

Start with the **integrity grade**: every project carries an A, B or C grade over the range you
look at, with the reasons that set it, computed from what the platform's edge observes (the
class of each visitor's network, distinct networks, the quarantine band, proof failures and the
site's own baseline). Sort the global web table by it, filter it to **C** or **B and below** to
find the projects to look at first, and open the reasons panel before you rely on a project's
numbers. The project sees the same grade and reasons on its own overview.
The rules are in [bot and inflation defences](../security/bot-and-inflation.md) section 9.

The grade is automatic; you decide what it means for a project. A project that disagrees with its
grade opens an **Integrity appeal** issue from its console, prefilled with its slug, sites, period,
grades, reason codes and rule set version. Read the appeal next to the reasons panel of the same
range, answer on the issue, and record your decision there. The stored grade does not change; when
an appeal shows that a rule misreads a kind of traffic, the fix is a new rule set version
([appealing a grade](../security/bot-and-inflation.md#appealing-a-grade)).

Then read the **Not measured** column: the estimated share of the project's visits that are not
measured because the visitor's browser sends Global Privacy Control (Brave, DuckDuckGo, Firefox
private windows). These visitors are left out by design, and their share differs between
projects: developer tools and privacy-minded audiences lose more. When you compare two projects,
compare their counts with this gap in mind. The share appears from 100 loads of the tracker file
in the range; the method and its limits are in the
[privacy strategy](../compliance/privacy-strategy.md), section 6.

Three properties of the data shape how you compare projects: bands, daily-unique visitors, and
span-derived API data. Each follows from a privacy or design choice. The full definitions are in
[web metrics](../reference/web-metrics.md) and sections 7.3 and 8 of the
[control plane API reference](../reference/control-plane-api.md).

## Bands: primary and quarantine

Every site has two bands, each an Umami website of its own: the **primary band** and the
**quarantine band**.

- The gateway counts, per site and per truncated address range (an IPv4 /24 or IPv6 /48,
  keyed with a secret that changes daily), how many distinct visitors it sees within a
  sliding hour. From the event that brings that count to the threshold onward, events from that
  range go to the quarantine band for the rest of the window. The threshold follows the
  network's class and the site's size: 20 for an access network of a new site, more for a
  popular site whose audience shares carrier addresses, and lower for hosting networks and
  anonymisers such as Tor exits (5 on a new site). Every gateway shares these counts through NATS, so the threshold holds
  across nodes.
- Quarantine is a separate band, never a deletion. It is shown next to the primary numbers in
  the global views (quarantined page views, visitors and share), and the project sees it on its
  own web dashboard by switching to the quarantined band.
- Shared networks can trip it too: VPN exits, offices, campuses and conference Wi-Fi, and
  carrier-grade NAT on a site that has no baseline yet. A high quarantined share can be real
  people on such networks, or one source producing many visitor identities to inflate the numbers.
- How to read it: compare projects on the primary band, and look at a project's quarantined
  share next to it. A project whose quarantined share is far above others' deserves a look at
  its gateway outcomes and dates before its primary numbers are trusted.

The rejected share is separate: requests the gateway refused (missing or wrong proof of work,
an origin the site did not register, replayed proofs, rate limits, and browsers that send
Global Privacy Control, which are dropped by design). Rejected requests never reach any band.

What the checks catch is in [bot and inflation defences](../security/bot-and-inflation.md). In short:
naive server-side posts are rejected, and many identities from one network are quarantined,
but a determined actor with a real browser on many residential addresses passes every check.

## Daily-unique visitors

No cookie and no stored address is used. A visitor is recognised only by
a token computed from the address, the user agent, the site and a secret that exists for one
UTC day and is then destroyed. Umami's own salt also changes daily.

Consequences:

- Visitors are unique per UTC day and per site. A person who visits on Tuesday and Wednesday
  counts twice. Visitors over a range are the sum of the daily visitors; this equals what
  Umami answers for the same range (measured, `TestWebPath/time_ranges` and
  `TestAggregateWeb`).
- A person who visits two sites of the same project counts once per site.
- A changed user agent (a browser update) or a changed address (mobile to Wi-Fi) is a new
  visitor that day. Two browsers behind one address are two visitors.
- A visit that runs across midnight UTC splits into two visits of two visitors.
- A restart of the whole NATS cluster during a day destroys that day's secret early, so
  visitors seen earlier that day are counted again after it.

How to read it: over a 30-day range, "visitors" is visitor-days, not people. It is a fair
comparison between projects, because every project is counted the same way, but it is not a
count of distinct people and must not be presented as one. Page views and visits are not
affected by the daily rule.

## Visits, bounce rate and visit duration

- A visit ends after more than 30 minutes without an event, with one exception that comes
  from Umami: two events in the same UTC hour stay in one visit even with a longer gap
  (the visit rule in [web metrics](../reference/web-metrics.md)).
- Bounce rate is visits with exactly one page view and no custom event, over all visits.
- Visit duration is the time between the first and last page view of a visit. The time spent
  on the last page is never measured, so a visit of one page has duration 0.

## Span-derived API data

Projects send API data to the collector in one of two ways, and the collector keeps both
apart:

- **Metrics** (`http.server.request.duration`). These count every request.
- **Spans** (traces). The collector turns server spans into the same histogram and then drops
  the spans. If the project samples its traces, only the sampled requests are counted.

For each service and over the whole selected range, the numbers use metrics when the service
sent any in the range, and spans otherwise. The two are never added. The global API view shows
the source per project (`Metrics`, `From traces`, or `Metrics and traces` when its services
differ), and the project's own API dashboard marks span-derived services with a "from
traces" badge.

How to read it: request counts from traces are a lower bound when traces are sampled, so a
project that exports only traces can look smaller than it is, and its counts are not
comparable with a project that exports metrics. Ask such a project to add metric export (the
[API quickstart](../onboarding/api/quickstart.md) shows how) before comparing request volumes.

API numbers leave infrastructure requests out: CORS preflights, health and probe routes, and
requests from known health checkers. Each row shows them apart as `infrastructure_requests`, so a
project whose load balancer checks it every few seconds compares on its users' traffic alone
([traffic classes](../reference/collector.md#traffic-classes)). Rows stored before the
platform classified requests count every request as application traffic.

Two more flags in the API numbers:

- **Approximate percentiles** (`lossy`): the project sent histograms with other bucket bounds,
  which were re-bucketed onto the standard layout. Percentiles are then approximate.
- **`>10 s`**: the percentile falls above the last bucket bound, 10 seconds. No number above
  10 s is ever invented.

## Freshness and gaps

Web aggregates are read from Umami hourly for today and the three days before, so a late
backlog (after an outage, events are queued for up to 72 hours and replayed with their
original times) shows up within an hour of being delivered for those days. A day older than
that is not read again unless someone runs a backfill (`jobs run aggregate-web --from --to`).
In Compare, a day that was never read is a gap, not a zero.

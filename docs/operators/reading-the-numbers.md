# What the numbers mean

Three properties of the data change how cross-project numbers should be read: bands,
daily-unique visitors, and span-derived API data. Each follows from a privacy or design
choice, not from a defect. Sources: `docs/reference/web-metrics.md`,
`docs/reference/control-plane-api.md` sections 7.3 and 8, and the design

## Bands: primary and quarantine

Every site has two web dashboards: the **primary band** and the **quarantine band**.

- The gateway counts, per site and per truncated address range (an IPv4 /24 or IPv6 /48,
  keyed with a secret that changes daily), how many distinct visitors it sees within a
  sliding hour. From the event that brings that count to the threshold (`IPCLUSTER_THRESHOLD`,
  default 20) onward, events from that range go to the quarantine band for the rest of the
  window. The three gateways share these counts, so the threshold holds across them.
- Quarantine is a separate band, never a deletion. It is shown next to the primary numbers in
  the global views (quarantined page views, visitors and share) and the project sees it in its
  own quarantine dashboard.
- Shared networks trip it too: carrier-grade NAT on mobile networks, VPN exits, offices,
  campuses and conference Wi-Fi. A high quarantined share can be real people on such networks,
  or one source producing many visitor identities to inflate the numbers.
- How to read it: compare projects on the primary band, and look at a project's quarantined
  share next to it. A project whose quarantined share is far above others' deserves a look at
  its gateway outcomes and dates before its primary numbers are trusted.

The rejected share is separate: requests the gateway refused (missing or wrong proof of work,
an origin the site did not register, replayed proofs, rate limits, and browsers that send
Global Privacy Control, which are dropped by design). Rejected requests never reach any band.

What the checks can and cannot catch is in `docs/security/bot-and-inflation.md`. In short:
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
  (`docs/reference/web-metrics.md`, the visit rule).
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
comparable with a project that exports metrics. Ask such a project to add metric export (the API onboarding
guides in `docs/onboarding/` show how) before comparing request volumes.

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

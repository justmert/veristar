# Reading the global views

The global views compare every enrolled project on the RFP's core metrics, for funding and
ecosystem-visibility decisions (RFP 3.2 and 3.3). They read daily aggregates only, never raw
events or sessions. The contract is `docs/reference/control-plane-api.md` section 8. What the
numbers mean, and where they can mislead, is in [reading-the-numbers.md](reading-the-numbers.md);
read it before drawing conclusions.

## Range

Every view has a range of whole UTC days, both ends included: presets of 7, 30, 90 and 365
days ending today, or **Custom** with a from and to day. The longest range is 366 days. Today
is included and holds what the hourly aggregation has read so far, so today's numbers grow
during the day. Without a choice the range is the last 30 days.

## Global web

One row per project, summed over its sites, for the primary band unless a column says
quarantined.

| Column | Meaning |
|---|---|
| Page views | page view events |
| Visitors | sum of daily unique visitors over the range (see daily-unique visitors) |
| Visits | visits with at least one page view |
| Bounce rate | visits with exactly one page view and no custom event, over all visits |
| Average visit duration | time between the first and last page view of a visit, averaged; the time on the last page is not measured |
| Quarantined page views, visitors, share | the quarantine band, and its share of all page views |
| Gateway requests, rejected, rejected share | every request the gateway answered for the project's sites, and those it refused |
| Sites, verified sites, sites with stellar.toml | the project's non-removed sites and the evidence on them |

Each row also carries the top five referrers, countries, devices and browsers over the range.

Filters (all must hold): status, name or slug, domain verified, stellar.toml found, has web
data in the range, has API data in the range, enrollment date range. Sort by any column,
largest or smallest first; projects without a value for the sort column (a rate with no
visits) come last either way.

## Global API

One row per project.

| Column | Meaning |
|---|---|
| Requests | requests over every service of the project |
| 4xx, 5xx and their rates | requests answered with status 400 to 499 and 500 to 599; a request without a status code counts in requests only |
| p50, p95, p99 | latency percentiles from the summed histogram buckets of the range, in seconds; `>10 s` when the percentile is above the last bucket bound |
| Services, routes | distinct services, and distinct normalized routes |
| Source | `Metrics`, `From traces` or `Metrics and traces` (see span-derived data) |

Sorting by requests in each direction finds the highest and lowest usage projects. Percentiles
are never averaged across services or days; they are computed from bucket counts summed over
exactly what the row covers.

## Compare

Tick up to five projects on Global web or Global API, then choose **Compare**. The compare
page shows, for the same range:

- web: daily charts of page views, visitors, bounce rate and average visit duration, a totals
  table, and the largest referrers, countries, devices, browsers and operating systems of each
  project;
- API: daily charts of requests, p95 latency and the 4xx and 5xx rates, a totals table
  (requests, rates, p50, p95, p99, services, routes), and every
  service of each project ordered by requests, so the first and last are the most and least
  used services.

A day that the aggregation has not read yet (for example before a backfill) is a gap in the
web charts and shows as not read in the tables. A day read without traffic shows 0. Compare
with one project selected is that project's aggregate drill-down.

The compare page's address holds the selected projects and the range, so a comparison can be
shared with another operator as a link.

## Freshness

Web numbers are copied from Umami's own answers once an hour, for today and the three days
before. API numbers come from the rolled-up hours and days, and hours not yet rolled up are
computed from their minutes, so API answers do not wait for the rollup.

## The same numbers for machines

The metrics API (`/api/metrics/v1/`, [api-keys.md](api-keys.md)) returns the same daily
numbers as these views for downstream consumers.

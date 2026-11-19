---
title: "Reading the global views"
description: "Compare every enrolled project on the core web and API metrics."
---

The global views compare every enrolled project on the core web and API metrics, for funding and
ecosystem-visibility decisions. They are the **Web** and **API** views of the operator
**All projects** page (`/operator/projects?view=web` and `?view=api`). They read daily aggregates only, never raw events or sessions.
What the numbers mean, and how to compare projects fairly, is in
[reading-the-numbers.md](reading-the-numbers.md); read it before drawing conclusions. The HTTP
routes are section 8 of the [control plane API reference](../reference/control-plane-api.md).

## Range

Every view covers a range of whole UTC days, both ends included: presets of 7, 30, 90 and 365
days ending today, or **Custom** with a from and to day. The longest range is 366 days. Today
is included and holds what the hourly aggregation has read so far, so today's numbers grow
during the day. Without a choice the range is the last 30 days.

## Web view

One row per project, summed over its sites, for the primary band unless a column says
quarantined.

| Column | Meaning |
|---|---|
| Page views | page view events |
| Visitors | sum of daily unique visitors over the range (see daily-unique visitors) |
| Visits | visits with at least one page view |
| Bounce rate | visits with exactly one page view and no custom event, over all visits |
| Average visit | time between the first and last page view of a visit, averaged; the time on the last page is not measured |
| Held back page views, Held back visitors, Held back | the quarantine band, and its share of all page views |
| Gateway requests, Refused requests, Refused | every request the gateway answered for the project's sites, and those it refused |
| Not measured | the estimated share of visits the tracker does not measure because the browser sends Global Privacy Control, from the tracker file requests of the range; shown from 100 requests on, a dash below that, with the requests carrying the signal and all requests on the line under it ([reading-the-numbers.md](reading-the-numbers.md)) |
| Sites, Verified sites, Sites with stellar.toml | the project's non-removed sites and the evidence on them |

Each row also carries the top five referrers, countries, devices and browsers over the range.

Each row also carries the project's integrity grade (see
[reading-the-numbers.md](reading-the-numbers.md)) and its on-chain column with weekly active
accounts ([onchain-and-exports.md](onchain-and-exports.md)).

Filters (all must hold): status, name or slug, domain verified, stellar.toml found, has web
data in the range, has API data in the range, enrollment date range, the cohort filters
(below), and **Grade**: **C** lists the projects graded C over the range, **B or C**
those graded B or C, **Any grade given** every project with a grade. Sort by any column,
largest or smallest first; projects without a value for the sort column (a rate with no
visits) come last either way.

## API view

One row per project.

| Column | Meaning |
|---|---|
| Requests | application requests over every service of the project; the line under the count shows the infrastructure requests (CORS preflights, health and probe routes, known health checkers) left out of it |
| 4xx responses, 5xx responses, 4xx rate, 5xx rate | requests answered with status 400 to 499 and 500 to 599; a request without a status code counts in requests only |
| p50 latency, p95 latency, p99 latency | latency percentiles from the summed histogram buckets of the range, in seconds; `>10 s` when the percentile is above the last bucket bound |
| Services, routes | distinct services, and distinct normalized routes |
| Data source | `Metrics`, `Traces` or `Metrics and traces` (see span-derived data) |

Requests, error rates and latency count application requests only; infrastructure requests are
shown apart and never mix into them. Sorting by requests in each direction finds the highest and
lowest usage projects. Percentiles are never averaged across services or days; they are computed from bucket counts summed over
exactly what the row covers.

## Growth and percentiles

Both tables carry five key metrics for every project: verified visitors (the primary band's
visitors), page views, application requests, error rate (application requests answered 500 to
599 over all application requests) and on-chain operations. For each one you see:

- **Change** (for example **Visitors change**): the value against the previous period of equal length, the days just before the
  range. For the four counts it is relative (`+50%` means one and a half times the previous
  period); for the error rate it is the difference in percentage points. A project without a
  previous value shows no change rather than an infinite one.
- **Percentile** (for example **Visitors percentile**): the share of the cohort's projects, among those with a value, whose value is at
  or below this project's. 100th means the largest value in the cohort. A project with no page
  views, no requests or no on-chain operations has no percentile for that metric. For the error
  rate, a higher percentile is a higher error rate.

Sort by any change to find the fastest growing projects, or by any percentile. The percentiles
come from the daily aggregates in SQL, in one snapshot with the rest of the table.

## Cohorts

SCF decides by program, round, category and award date, so every project carries those four
values. They come from three sides:

1. A project proposes them, at enrollment or later on its **SCF review** page.
2. The SCF award matching proposes them for a matched project that has no proposal
   ([scf-awards.md](scf-awards.md)).
3. You confirm the proposal on the project's detail page, edit it before confirming, set the
   values yourself, or dismiss the proposal. Only confirmed values count.

Every proposal, confirmation, change and dismissal is in the audit log with the old and new
values. Pending proposals are listed at the top of the project detail and in `GET
/api/operator/cohorts`.

The cohort filters (program, round, category, award date from and to) narrow both tables and set
the population of the percentiles: with **Build Award** and **SCF #38** selected, every
percentile is computed among the active and suspended projects confirmed in that program and
round. The other filters never move a percentile: searching for one project still shows its place
in the whole cohort. Without a cohort filter the population is every active and suspended
project.

## Award dates on the charts

A project's confirmed award date is drawn as a vertical line on the compare charts and on the
project's own charts, so growth after funding is visible at a glance. The compare CSV marks every
day on or after the award date in its `after_award` column.

## Threshold rules

A rule states a traction threshold once, and the console lists the projects that meet it with
the evidence. See [threshold rules](threshold-rules.md).

## Compare

Tick up to five projects in the Web or API view, then choose **Compare**. The compare
page shows, for the same range:

- web: daily charts of page views, visitors, bounce rate and average visit duration, a totals
  table, and the largest referrers, countries, devices, browsers and operating systems of each
  project;
- API: daily charts of requests, p95 latency and the 4xx and 5xx rates, a totals table
  (requests, rates, p50, p95, p99, services, routes), and every service of each project ordered
  by requests, so the first and last are the most and least used services. As in the API view of
  All projects, these count application requests only, with the infrastructure requests shown apart.

A day that the aggregation has not read yet (for example before a backfill) is a gap in the
web charts and shows as not read in the tables. A day read without traffic shows 0. Compare
with one project selected is that project's aggregate drill-down.

The compare page's address holds the selected projects and the range, so a comparison can be
shared with another operator as a link.

## Freshness

Web numbers are copied from Umami's own answers once an hour, for today and the three days
before. API numbers come from the rolled-up hours and days, and hours not yet rolled up are
computed from their minutes, so API answers do not wait for the rollup.

## Exports

Every table and comparison exports as CSV with the columns on screen, including the cohort, the
previous values, the changes and the percentiles. The project sees its own row, cohort,
percentiles and the rules it meets on its **SCF review** page, so nothing here is hidden scoring.

## The same numbers for machines

The metrics API (`/api/metrics/v1/`, [api-keys.md](api-keys.md)) returns the same daily
numbers as these views for downstream consumers, and the saved rules with their matches.

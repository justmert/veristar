---
title: "Investigating suspected metric inflation"
sidebarTitle: "Metric inflation"
description: "Investigate a project whose numbers look higher than its real use."
icon: "arrow-trend-up"
---

A project's numbers look higher than its real use: someone reports it, an operator comparing
projects for funding doubts a jump, or its integrity grade drops. Every site already carries an
integrity grade per UTC day, A, B or C, with the reasons that set it, computed from what the edge
observes ([../security/bot-and-inflation.md](../security/bot-and-inflation.md) section 9). Start
from that report: it names the evidence, the days and the figures. This runbook takes you from the
grade to a written finding the operators can act on: what was counted, what evidence points at
forgery, and what was done. Deleting counted data is not an operator action; the quarantine band,
the counters and the grade are shown next to the numbers instead.

All SQL below runs on the database leader ([overview.md](overview.md)), database `platform` unless
it says `umami`. Replace `<tenant id>`, `<site id>` and the dates.

## Web traffic

1. **The integrity report.** In the console, open the project from the **All projects** page of
   the operator area and read its **Integrity report**: the range grade, the reasons with the number of
   days each applied to, and each site's days with their figures. The same report is
   `GET /api/operator/web/integrity?tenant=<tenant id>&from=<YYYY-MM-DD>&to=<YYYY-MM-DD>`, the
   MCP tool `get_tenant_integrity`, and the rows below:

   ```sql
   select site_id, day, grade, closed, events, quarantined, proof_failures,
     net_access, net_organisation, net_hosting, net_anonymiser, net_unknown,
     networks, visitors, pageviews, baseline_visitors, baseline_days, reasons
   from agg.web_integrity_daily
   where tenant_id = '<tenant id>' and day between '<YYYY-MM-DD>' and '<YYYY-MM-DD>'
   order by site_id, day;
   ```

   Read the reasons against the evidence: `hosting_share` with few `networks` is traffic from a
   handful of data centres; `quarantine_share` is crowded prefixes or over-active visitors;
   `spike` compares the day with the site's own median; `page_volume` is the page reporting far
   more page views than visitors. A grade of A with a jump in visitors is traffic from many
   distinct access networks: either a real audience or a forger paying for residential
   endpoints, which the ledger activity of the project helps to tell apart.

   After a backfill of the web aggregates, or to grade days outside the regular window again, run
   `jobs run integrity --from <YYYY-MM-DD> --to <YYYY-MM-DD>` on the jobs leader. A web
   aggregate backfill reaches back only to the raw retention cutoff (`WEB_RAW_RETENTION_MONTHS`):
   Umami has no raw rows before it, and `jobs run aggregate-web` refuses such a `--from`. A day
   older than the three-day regrade window has lost its network keys, so its regrade keeps the
   stored `rule_version` and `networks` and reads only the other inputs again; a day of that age
   that has no grade row is not graded at all, rather than graded on a network count of zero.

2. **Network evidence per day** while a day is still open (the network keys are deleted once the
   day leaves the regrade window, the counts stay in the grade row):

   ```sql
   select day, class, sum(count) as events
   from cp.web_evidence_daily
   where site_id = '<site id>' and day between '<YYYY-MM-DD>' and '<YYYY-MM-DD>'
   group by 1, 2 order by 1, 2;

   select day, count(*) as distinct_networks
   from cp.web_networks_daily
   where site_id = '<site id>' group by 1 order by 1;
   ```

3. **Outcome counters per day.** Every event the gateways saw is counted by outcome
   (`accepted`, `quarantined`, `bot`, `dlq`, `rejected_<reason>`):

   ```sql
   select site_id, (date_trunc('day', hour))::date as day,
     sum(count) filter (where outcome = 'accepted') as accepted,
     sum(count) filter (where outcome = 'quarantined') as quarantined,
     sum(count) filter (where outcome = 'bot') as bot,
     sum(count) filter (where outcome = 'rejected_replay') as replay,
     sum(count) filter (where outcome in ('rejected_proof_work', 'rejected_challenge_tag',
       'rejected_challenge_expired', 'rejected_challenge_future', 'rejected_missing_proof')) as bad_proof,
     sum(count) filter (where outcome in ('rejected_origin', 'rejected_origin_missing', 'rejected_signals')) as bad_headers,
     sum(count) filter (where outcome = 'rejected_rate_limited') as rate_limited
   from cp.web_ingest_hourly
   where tenant_id = '<tenant id>' and hour >= '<YYYY-MM-DD>' and hour < '<YYYY-MM-DD>'
   group by 1, 2 order by 1, 2;
   ```

   Signs of forging: a rise in `quarantined` (more distinct visitor tokens from one address prefix
   within an hour than its network class and the site's size allow), in `replay` (a captured proof sent again), in `bad_proof` or `bad_headers` (naive
   server-side posts), or `rate_limited`. A rise in `accepted` with none of these is either real
   traffic or a capable forger (real browsers, residential proxies).

4. **Hourly shape.** Real traffic follows the time zones of its audience. Flat lines, exact
   round counts, or traffic that starts and stops on the minute are suspicious:

   ```sql
   select hour, outcome, count from cp.web_ingest_hourly
   where site_id = '<site id>' and hour >= '<YYYY-MM-DD>' and hour < '<YYYY-MM-DD>'
   order by hour, outcome;
   ```

5. **Primary and quarantine bands side by side** (the operator aggregates read from Umami):

   ```sql
   select day, band, pageviews, visitors, visits, bounces, totaltime
   from agg.web_daily
   where site_id = '<site id>' and day between '<YYYY-MM-DD>' and '<YYYY-MM-DD>'
   order by day, band;
   ```

   A sudden quarantine band next to a flat primary band is the same-IP check working. Pageviews
   per visit near 1 with bounces near visits and `totaltime` near 0 is a common pattern of
   scripted pageviews.

6. **Concentration.** Forged traffic tends to come from few browsers, operating systems, devices,
   countries or referrers:

   ```sql
   select day, band, dimension, key, value
   from agg.web_daily_dim
   where site_id = '<site id>' and day between '<YYYY-MM-DD>' and '<YYYY-MM-DD>'
   order by day, band, dimension, value desc;
   ```

7. **Raw events, when the aggregates are not enough.** Database `umami`, both of the site's
   websites (the primary has the site id; the quarantine id is `cp.sites.quarantine_website_id`):

   ```sql
   select date_trunc('hour', e.created_at) as hour, count(*) as events,
     count(distinct e.session_id) as sessions, count(distinct e.url_path) as paths,
     count(distinct s.browser || '/' || s.os || '/' || s.screen) as client_shapes
   from website_event e join session s using (session_id)
   where e.website_id = '<site id>' and e.created_at >= '<YYYY-MM-DD>' and e.created_at < '<YYYY-MM-DD>'
   group by 1 order by 1;
   ```

   Many sessions with one identical client shape and one path per session within an hour is
   scripted traffic. No address is stored anywhere, so the investigation cannot and does not
   identify who sent it.

## API traffic

API numbers come from the tenant's own OTLP export with its own token; a tenant can inflate them
by sending made-up metrics. There is no proof-of-browser equivalent.

1. **Volume per service and day:**

   ```sql
   select (date_trunc('day', bucket_start))::date as day, service, source,
     sum(count) as requests, count(distinct route) as routes
   from api.metrics_hourly
   where tenant_id = '<tenant id>' and bucket_start >= '<YYYY-MM-DD>' and bucket_start < '<YYYY-MM-DD>'
   group by 1, 2, 3 order by 1, 2, 3;
   ```

2. **Warnings** the writer recorded (cardinality overflow, lossy histograms, span-derived data):

   ```sql
   select day, kind, count from api.ingest_warnings
   where tenant_id = '<tenant id>' order by day desc, kind;
   ```

3. **Plausibility.** Compare requests per day with the status mix and the latency histogram:
   made-up metrics often have one status code, one bucket, or perfectly even minutes. Minute rows
   are kept for `API_MINUTE_RETENTION_DAYS` (30 by default) in `api.metrics_minute` with the same
   columns.

4. **Tokens.** `select id, name, status, created_at, last_seen_at from cp.otlp_tokens where tenant_id = '<tenant id>';`
   Stored metric rows do not carry the token, so a token cannot be tied to particular rows.

## Acting on a finding

- **Ask the project.** Most jumps are launches, campaigns or a crawler they run themselves.
- **Suspend** while the question is open ([tenant-removal.md](tenant-removal.md)): ingestion stops,
  data stays.
- **Raise the cost for everyone** when one site is targeted by naive forging: raise `PROOF_BITS`
  (12 today) or lower `IPCLUSTER_THRESHOLD` (20 today, the floor of the access threshold; hosting
  networks get a quarter of it) in `edge_gateway_env`
  (`infra/ansible/roles/edge/defaults/main.yml`, or an override of the whole map in
  `infra/ansible/inventories/<env>/group_vars/all.yml`), record the reason in
  [../reference/gateway-limits.md](../reference/gateway-limits.md), and roll out with
  `make <env>-deploy ROLLING=1 ...`. A higher `PROOF_BITS` costs every visitor's browser CPU;
  measure first (`pnpm --filter tracker bench:proof`). A lower floor quarantines more shared
  addresses on sites without a baseline yet; sites with a baseline keep their scaled threshold.
- **Tell the project.** The project sees the same grade and reasons in the **Integrity report**
  of its **SCF review** page; point it
  at the days and reasons in question.
- **Record the finding** with dates, the integrity report, the queries' output and the decision, where the operators
  keep their review notes, and mention it when the project's numbers are compared.

## Exercised

Every query above ran against the real schemas, with placeholders replaced by a random uuid and
a date range:

| Date | What ran | Result |
|---|---|---|
| 2026-11-13 | the seven `platform` queries on a scratch PostgreSQL 18.6 database at migration 22 (`platformctl db migrate` reported `applied 22`, `rls_coverage ok`) | every query ran without error (empty results) |
| 2026-11-13 | the `umami` query on the development stack's Umami 3.5.0 database, read only | ran without error |
| 2026-11-16 | the integrity, evidence and network queries on a scratch PostgreSQL 18.7 database at migration 33 | every query ran without error |

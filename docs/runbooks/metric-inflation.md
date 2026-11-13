# Investigating suspected metric inflation

A project's numbers look higher than its real use: someone reports it, an operator comparing
projects for funding doubts a jump, or the outcome counters move. What the platform can and
cannot catch is stated in [../security/bot-and-inflation.md](../security/bot-and-inflation.md);
read section 9 ("Limits, stated plainly") first. The checks raise the cost of forged traffic and
make it visible; they do not prove that every counted visit was a person.

The aim of an investigation is a written finding the operators can act on: what was counted,
what evidence points at forgery, and what was done. Deleting counted data is not an operator
action; quarantine bands and counters are shown next to the numbers instead.

All SQL below runs on the database leader ([README.md](README.md)), database `platform` unless
it says `umami`. Replace `<tenant id>`, `<site id>` and the dates.

## Web traffic

1. **Outcome counters per day.** Every event the gateways saw is counted by outcome
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

   Signs of forging: a rise in `quarantined` (many visitor tokens from one address prefix within
   an hour), in `replay` (a captured proof sent again), in `bad_proof` or `bad_headers` (naive
   server-side posts), or `rate_limited`. A rise in `accepted` with none of these is either real
   traffic or a capable forger (real browsers, residential proxies).

2. **Hourly shape.** Real traffic follows the time zones of its audience. Flat lines, exact
   round counts, or traffic that starts and stops on the minute are suspicious:

   ```sql
   select hour, outcome, count from cp.web_ingest_hourly
   where site_id = '<site id>' and hour >= '<YYYY-MM-DD>' and hour < '<YYYY-MM-DD>'
   order by hour, outcome;
   ```

3. **Primary and quarantine bands side by side** (the operator aggregates read from Umami):

   ```sql
   select day, band, pageviews, visitors, visits, bounces, totaltime
   from agg.web_daily
   where site_id = '<site id>' and day between '<YYYY-MM-DD>' and '<YYYY-MM-DD>'
   order by day, band;
   ```

   A sudden quarantine band next to a flat primary band is the same-IP check working. Pageviews
   per visit near 1 with bounces near visits and `totaltime` near 0 is a common pattern of
   scripted pageviews.

4. **Concentration.** Forged traffic tends to come from few browsers, operating systems, devices,
   countries or referrers:

   ```sql
   select day, band, dimension, key, value
   from agg.web_daily_dim
   where site_id = '<site id>' and day between '<YYYY-MM-DD>' and '<YYYY-MM-DD>'
   order by day, band, dimension, value desc;
   ```

5. **Raw events, when the aggregates are not enough.** Database `umami`, both of the site's
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
  (12 today) or lower `IPCLUSTER_THRESHOLD` (20 today) in `edge_gateway_env`
  (`infra/ansible/roles/edge/defaults/main.yml`, or an override of the whole map in
  `infra/ansible/inventories/<env>/group_vars/all.yml`), record the reason in
  [../reference/gateway-limits.md](../reference/gateway-limits.md), and roll out with
  `make <env>-deploy ROLLING=1 ...`. A higher `PROOF_BITS` costs every visitor's browser CPU;
  measure first (`pnpm --filter tracker bench:proof`). A lower threshold quarantines more shared
  addresses (carrier NAT, offices).
- **Record the finding** with dates, the queries' output and the decision, where the operators
  keep their review notes, and mention it when the project's numbers are compared.

## Exercised

**Not yet exercised as an investigation.** No suspected case exists: there is no production
traffic. this stage ran every query above against a scratch PostgreSQL 18.6 with all platform
migrations applied (the `platform` queries) and against the development stack's Umami 3.5.0
database (the `umami` query), with placeholders replaced by a random uuid and a date range,
to prove they are valid against the real schemas. Results are in the table below.

| Date | What ran | Result |
|---|---|---|
| 2026-11-13 | the seven `platform` queries on a scratch database at migration 22 (`platformctl db migrate` reported `applied 22`, `rls_coverage ok`) | every query ran without error (empty results) |
| 2026-11-13 | the `umami` query on the development stack's Umami database, read only | ran without error |

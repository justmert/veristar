-- integrity: grades every site and utc day from the evidence the gateways
-- reported, the outcome counters and the web aggregates. run in the fenced
-- jobs transaction (scheduler.Leader.Tx), under the tenant's shared agg
-- lock (LockAggTenant), like aggregate-web.

-- name: IntegrityOutcomes :one
-- accepted events of both bands, the quarantined part and the requests
-- refused for their proof of work, for one site and day
select coalesce(sum(count) filter (where outcome in ('accepted', 'quarantined')), 0)::bigint as events,
       coalesce(sum(count) filter (where outcome = 'quarantined'), 0)::bigint as quarantined,
       coalesce(sum(count) filter (where outcome in (
         'rejected_missing_proof', 'rejected_challenge_tag', 'rejected_challenge_expired',
         'rejected_challenge_future', 'rejected_proof_work', 'rejected_replay')), 0)::bigint as proof_failures
from cp.web_ingest_hourly
where site_id = @site_id and hour >= @from_hour::timestamptz and hour < @to_hour::timestamptz;

-- name: IntegrityEvidence :many
select class, count from cp.web_evidence_daily where site_id = @site_id and day = @day;

-- name: IntegritySignals :many
select signal, count from cp.web_signals_daily where site_id = @site_id and day = @day;

-- name: IntegrityNetworks :one
select count(*)::bigint from cp.web_networks_daily where site_id = @site_id and day = @day;

-- name: IntegrityWebDay :one
-- the primary band's visitors and page views; ok is false before
-- aggregate-web read the day
select coalesce(sum(visitors), 0)::bigint as visitors, coalesce(sum(pageviews), 0)::bigint as pageviews,
       (count(*) > 0)::boolean as ok
from agg.web_daily where site_id = @site_id and day = @day and band = 'primary';

-- name: IntegrityBaseline :many
-- primary visitors of the earlier days with data, newest first
select visitors from agg.web_daily
where site_id = @site_id and band = 'primary' and day < @day::date and day >= @since::date
order by day desc;

-- name: UpsertIntegrityDay :exec
insert into agg.web_integrity_daily (
  site_id, tenant_id, day, rule_version, grade, closed, events, quarantined, proof_failures,
  net_access, net_organisation, net_hosting, net_anonymiser, net_unknown, networks,
  visitors, pageviews, baseline_visitors, baseline_days, signal_views, framed, never_visible,
  reasons, computed_at)
values (
  @site_id, @tenant_id, @day, @rule_version, @grade, @closed, @events, @quarantined, @proof_failures,
  @net_access, @net_organisation, @net_hosting, @net_anonymiser, @net_unknown, @networks,
  @visitors, @pageviews, @baseline_visitors, @baseline_days, @signal_views, @framed, @never_visible,
  @reasons, now())
on conflict (site_id, day) do update
  set tenant_id = excluded.tenant_id, rule_version = excluded.rule_version, grade = excluded.grade,
      closed = excluded.closed, events = excluded.events, quarantined = excluded.quarantined,
      proof_failures = excluded.proof_failures, net_access = excluded.net_access,
      net_organisation = excluded.net_organisation, net_hosting = excluded.net_hosting,
      net_anonymiser = excluded.net_anonymiser, net_unknown = excluded.net_unknown,
      networks = excluded.networks, visitors = excluded.visitors, pageviews = excluded.pageviews,
      baseline_visitors = excluded.baseline_visitors, baseline_days = excluded.baseline_days,
      signal_views = excluded.signal_views, framed = excluded.framed, never_visible = excluded.never_visible,
      reasons = excluded.reasons, computed_at = excluded.computed_at;

-- name: PruneNetworkSightings :execrows
-- network keys of days that are no longer graded again; their count is in
-- agg.web_integrity_daily
delete from cp.web_networks_daily where day < @before::date;

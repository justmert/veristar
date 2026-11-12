-- cp_tenant query of GET /api/sites/{id}/ingest (control-plane-api 4.3.7)

-- name: IngestCounts :many
-- buckets are cut in utc whatever the session time zone is
select date_trunc(@bucket::text, hour, 'UTC')::timestamptz as at, outcome, sum(count)::bigint as total
from cp.web_ingest_hourly
where site_id = @site_id and hour >= @from_hour and hour < @to_hour
group by 1, 2
having sum(count) > 0
order by 1, 2;

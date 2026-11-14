-- api metrics storage. rights,
-- row-level security and the partition functions follow in 00016, so that
-- every grant of the api schema sits in one file. docs/reference/api-storage.md
-- describes every table for the writer and the jobs.
--
-- conventions shared by the three metric tables:
-- * one row per tenant, bucket start, service, method, route, status code and
--   source. an attribute the data point did not carry is stored as '' (text)
--   or 0 (status code), because a unique key over nullable columns would let
--   two rows for the same absent value exist.
-- * counts holds the 15 buckets of the canonical layout (4.11), count is
--   their sum, so the check below keeps every row self-consistent.
-- * source is 'metric' for http.server.request.duration and 'span' for the
--   span-derived metric. both are kept apart at every resolution (4.5).

-- +goose Up

-- +goose StatementBegin
-- adds two bucket arrays element by element. the upserts of the writer and
-- the rollups merge histograms with it. arrays of different shape or with
-- null elements are an error rather than a silently shorter or null result.
create function api.add_buckets(a bigint[], b bigint[]) returns bigint[]
language plpgsql immutable strict parallel safe
as $$
begin
  if array_ndims(a) is distinct from 1 or array_ndims(b) is distinct from 1
     or cardinality(a) <> cardinality(b) then
    raise exception 'api.add_buckets: arrays of different shape (% and % elements)',
      cardinality(a), cardinality(b)
      using errcode = 'array_subscript_error';
  end if;
  if array_position(a, null) is not null or array_position(b, null) is not null then
    raise exception 'api.add_buckets: null bucket count'
      using errcode = 'null_value_not_allowed';
  end if;
  return array(
    select x + y
    from unnest(a, b) with ordinality as t(x, y, i)
    order by i);
end
$$;
-- +goose StatementEnd

-- the strict transition function makes the first non-null array the
-- starting state, so the aggregate needs no initial array of a fixed size
create aggregate api.sum_buckets(bigint[]) (
  sfunc = api.add_buckets,
  stype = bigint[],
  combinefunc = api.add_buckets,
  parallel = safe
);

-- +goose StatementBegin
-- the q-quantile of a canonical histogram, by linear
-- interpolation inside the bucket that holds rank q * total. the first bucket
-- starts at 0 because durations are never negative. a quantile in the open
-- bucket above 10 s returns 'Infinity', which callers show as ">10 s" and
-- never as a number. an empty histogram has no quantile and returns null.
-- internal/histo.Quantile is the same computation in go, operation for
-- operation, and the two are tested against each other.
create function api.histogram_quantile(q double precision, counts bigint[]) returns double precision
language plpgsql immutable strict parallel safe
as $$
declare
  bounds constant double precision[] :=
    array[0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10];
  total bigint := 0;
  cum bigint := 0;
  prev bigint;
  c bigint;
  rank double precision;
  lower double precision;
begin
  -- nan compares greater than every number in postgresql, so q > 1 covers it
  if q < 0 or q > 1 then
    raise exception 'api.histogram_quantile: q % is outside [0, 1]', q
      using errcode = 'invalid_parameter_value';
  end if;
  if array_ndims(counts) is distinct from 1 or cardinality(counts) <> 15 then
    raise exception 'api.histogram_quantile: want 15 buckets, have %', cardinality(counts)
      using errcode = 'array_subscript_error';
  end if;
  for i in 1 .. 15 loop
    c := counts[i];
    if c is null or c < 0 then
      raise exception 'api.histogram_quantile: bucket % holds %', i, coalesce(c::text, 'null')
        using errcode = 'invalid_parameter_value';
    end if;
    total := total + c;
  end loop;
  if total = 0 then
    return null;
  end if;

  rank := q * total::double precision;
  for i in 1 .. 15 loop
    c := counts[i];
    prev := cum;
    cum := cum + c;
    -- the first non-empty bucket whose running total reaches the rank. empty
    -- buckets are skipped so that q = 0 lands in the first bucket with data.
    if c > 0 and cum::double precision >= rank then
      if i = 15 then
        return 'Infinity'::double precision;
      end if;
      lower := case when i = 1 then 0 else bounds[i - 1] end;
      return lower + (bounds[i] - lower) * ((rank - prev::double precision) / c::double precision);
    end if;
  end loop;
  -- unreachable: q <= 1 means rank <= total, which the last non-empty
  -- bucket reaches
  raise exception 'api.histogram_quantile: no bucket reached rank %', rank;
end
$$;
-- +goose StatementEnd

create table api.metrics_minute (
  tenant_id    uuid not null,
  bucket_start timestamptz not null check (bucket_start = date_trunc('minute', bucket_start, 'UTC')),
  service      text not null check (octet_length(service) <= 128),
  method       text not null check (octet_length(method) <= 16),
  route        text not null check (octet_length(route) <= 1024),
  status_code  smallint not null check (status_code = 0 or status_code between 100 and 599),
  source       text not null check (source in ('metric', 'span')),
  counts       bigint[] not null,
  sum          double precision not null,
  count        bigint not null,
  lossy        boolean not null default false,
  constraint metrics_minute_counts_check check (
    array_ndims(counts) = 1 and cardinality(counts) = 15 and array_position(counts, null) is null
    and 0 <= all (counts)
    and count = counts[1] + counts[2] + counts[3] + counts[4] + counts[5] + counts[6] + counts[7]
              + counts[8] + counts[9] + counts[10] + counts[11] + counts[12] + counts[13]
              + counts[14] + counts[15]),
  primary key (tenant_id, bucket_start, service, method, route, status_code, source)
) partition by range (bucket_start);

create table api.metrics_hourly (
  tenant_id    uuid not null,
  bucket_start timestamptz not null check (bucket_start = date_trunc('hour', bucket_start, 'UTC')),
  service      text not null check (octet_length(service) <= 128),
  method       text not null check (octet_length(method) <= 16),
  route        text not null check (octet_length(route) <= 1024),
  status_code  smallint not null check (status_code = 0 or status_code between 100 and 599),
  source       text not null check (source in ('metric', 'span')),
  counts       bigint[] not null,
  sum          double precision not null,
  count        bigint not null,
  lossy        boolean not null default false,
  constraint metrics_hourly_counts_check check (
    array_ndims(counts) = 1 and cardinality(counts) = 15 and array_position(counts, null) is null
    and 0 <= all (counts)
    and count = counts[1] + counts[2] + counts[3] + counts[4] + counts[5] + counts[6] + counts[7]
              + counts[8] + counts[9] + counts[10] + counts[11] + counts[12] + counts[13]
              + counts[14] + counts[15]),
  primary key (tenant_id, bucket_start, service, method, route, status_code, source)
) partition by range (bucket_start);

-- kept forever, so it is not partitioned
create table api.metrics_daily (
  tenant_id    uuid not null,
  bucket_start timestamptz not null check (bucket_start = date_trunc('day', bucket_start, 'UTC')),
  service      text not null check (octet_length(service) <= 128),
  method       text not null check (octet_length(method) <= 16),
  route        text not null check (octet_length(route) <= 1024),
  status_code  smallint not null check (status_code = 0 or status_code between 100 and 599),
  source       text not null check (source in ('metric', 'span')),
  counts       bigint[] not null,
  sum          double precision not null,
  count        bigint not null,
  lossy        boolean not null default false,
  constraint metrics_daily_counts_check check (
    array_ndims(counts) = 1 and cardinality(counts) = 15 and array_position(counts, null) is null
    and 0 <= all (counts)
    and count = counts[1] + counts[2] + counts[3] + counts[4] + counts[5] + counts[6] + counts[7]
              + counts[8] + counts[9] + counts[10] + counts[11] + counts[12] + counts[13]
              + counts[14] + counts[15]),
  primary key (tenant_id, bucket_start, service, method, route, status_code, source)
);

-- last cumulative point of each stream, for cumulative to delta. start times
-- stay in unix nanoseconds: timestamptz keeps microseconds only, and two
-- replicas whose start times differ below that would merge into one stream.
-- last_counts are on the canonical layout; re-bucketing is linear, so the
-- delta of re-bucketed points equals the re-bucketed delta.
create table api.series_state (
  tenant_id       uuid not null,
  series_hash     bytea not null check (octet_length(series_hash) = 32),
  start_unix_nano bigint not null check (start_unix_nano >= 0),
  last_unix_nano  bigint not null check (last_unix_nano >= 0),
  last_count      bigint not null check (last_count >= 0),
  last_sum        double precision not null,
  last_counts     bigint[] not null check (
    array_ndims(last_counts) = 1 and cardinality(last_counts) = 15
    and array_position(last_counts, null) is null and 0 <= all (last_counts)),
  last_seen_at    timestamptz not null,
  primary key (tenant_id, series_hash, start_unix_nano)
);
-- retention removes rows idle for 7 days
create index series_state_last_seen_idx on api.series_state (last_seen_at);

-- resources (scf.resource.hash) the writer has followed, for the first-point
-- rule of the metrics writer
create table api.resource_seen (
  tenant_id       uuid not null,
  resource_hash   bytea not null check (octet_length(resource_hash) = 32),
  start_unix_nano bigint not null check (start_unix_nano >= 0),
  first_seen_at   timestamptz not null,
  last_seen_at    timestamptz not null check (last_seen_at >= first_seen_at),
  primary key (tenant_id, resource_hash, start_unix_nano)
);
create index resource_seen_last_seen_idx on api.resource_seen (last_seen_at);

-- one row per applied API_METRICS message, keyed by Nats-Msg-Id. the tenant
-- comes from the subject; it lets a tenant purge remove its rows and puts the
-- table under the same row-level security rules as every other api table.
create table api.ingest_dedup (
  msg_id     text primary key check (octet_length(msg_id) between 1 and 256),
  tenant_id  uuid not null,
  applied_at timestamptz not null default now()
);
create index ingest_dedup_applied_at_idx on api.ingest_dedup (applied_at);

-- routes admitted per tenant and utc day, for the route and service caps
create table api.route_registry (
  tenant_id  uuid not null,
  day        date not null,
  service    text not null check (octet_length(service) <= 128),
  route      text not null check (octet_length(route) <= 1024),
  created_at timestamptz not null default now(),
  primary key (tenant_id, day, service, route)
);
create index route_registry_day_idx on api.route_registry (day);

create table api.ingest_warnings (
  tenant_id uuid not null,
  day       date not null,
  kind      text not null
            check (kind in ('cardinality_overflow', 'lossy_histogram', 'secret_in_path', 'span_derived')),
  count     bigint not null check (count > 0),
  primary key (tenant_id, day, kind)
);

-- hours the writer touched and the rollup has not yet recomputed. the writer
-- marks an hour in the transaction that writes its minute rows; the rollup
-- deletes the marks it takes and recomputes those hours, and the days that
-- contain them, in one transaction. a mark written while the rollup runs
-- either waits for it on the row lock and then survives, or is seen by it.
create table api.rollup_pending (
  tenant_id uuid not null,
  hour      timestamptz not null check (hour = date_trunc('hour', hour, 'UTC')),
  marked_at timestamptz not null default now(),
  primary key (tenant_id, hour)
);
create index rollup_pending_hour_idx on api.rollup_pending (hour);

-- +goose Down
drop table api.rollup_pending;
drop table api.ingest_warnings;
drop table api.route_registry;
drop table api.ingest_dedup;
drop table api.resource_seen;
drop table api.series_state;
drop table api.metrics_daily;
drop table api.metrics_hourly;
drop table api.metrics_minute;
drop function api.histogram_quantile(double precision, bigint[]);
drop aggregate api.sum_buckets(bigint[]);
drop function api.add_buckets(bigint[], bigint[]);

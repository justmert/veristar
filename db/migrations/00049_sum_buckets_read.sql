-- api.sum_buckets_read: the histogram sum of the read routes, safe over
-- any range. api.sum_buckets (00015) adds bucket counts in bigint, which a
-- tenant whose points carry the largest counts the writer accepts
-- (10^7 requests a point) passes within a few months; a 366-day list or
-- compare then failed for every tenant in it (22003 numeric value out of
-- range), and api.histogram_quantile sums its buckets in bigint as well.
-- this aggregate adds in numeric and, when the total passes 2^62, divides
-- every bucket by the same factor ceil(total / 2^62), rounding down, so
-- the result fits bigint, its total fits histogram_quantile, and every
-- quantile keeps its bucket and its position inside it up to one part in
-- 2^62. below 2^62 the result equals api.sum_buckets exactly. the rollups
-- keep api.sum_buckets: a stored row must hold the exact counts.

-- +goose Up
-- +goose StatementBegin
create function api.add_buckets_numeric(state numeric[], b bigint[]) returns numeric[]
language plpgsql immutable parallel safe
as $$
begin
  if b is null then
    return state;
  end if;
  if array_ndims(b) is distinct from 1 or (state is not null and cardinality(state) <> cardinality(b)) then
    raise exception 'api.sum_buckets_read: arrays of different shape (% and % elements)',
      cardinality(state), cardinality(b)
      using errcode = 'array_subscript_error';
  end if;
  if array_position(b, null) is not null then
    raise exception 'api.sum_buckets_read: null bucket count'
      using errcode = 'null_value_not_allowed';
  end if;
  if state is null then
    return b::numeric[];
  end if;
  return array(
    select x + y
    from unnest(state, b::numeric[]) with ordinality as t(x, y, i)
    order by i);
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
create function api.merge_buckets_numeric(a numeric[], b numeric[]) returns numeric[]
language plpgsql immutable parallel safe
as $$
begin
  if a is null then
    return b;
  end if;
  if b is null then
    return a;
  end if;
  if cardinality(a) <> cardinality(b) then
    raise exception 'api.sum_buckets_read: arrays of different shape (% and % elements)',
      cardinality(a), cardinality(b)
      using errcode = 'array_subscript_error';
  end if;
  return array(
    select x + y
    from unnest(a, b) with ordinality as t(x, y, i)
    order by i);
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
create function api.fit_buckets(state numeric[]) returns bigint[]
language plpgsql immutable strict parallel safe
as $$
declare
  cap constant numeric := 4611686018427387904; -- 2^62
  total numeric;
  k numeric;
begin
  select coalesce(sum(x), 0) into total from unnest(state) as t(x);
  k := greatest(ceil(total / cap), 1);
  return array(
    select floor(x / k)::bigint
    from unnest(state) with ordinality as t(x, i)
    order by i);
end
$$;
-- +goose StatementEnd

create aggregate api.sum_buckets_read(bigint[]) (
  sfunc = api.add_buckets_numeric,
  stype = numeric[],
  combinefunc = api.merge_buckets_numeric,
  finalfunc = api.fit_buckets,
  parallel = safe
);

revoke all on function api.add_buckets_numeric(numeric[], bigint[]) from public;
revoke all on function api.merge_buckets_numeric(numeric[], numeric[]) from public;
revoke all on function api.fit_buckets(numeric[]) from public;
revoke all on function api.sum_buckets_read(bigint[]) from public;
grant execute on function api.add_buckets_numeric(numeric[], bigint[]) to cp_tenant, cp_operator, jobs;
grant execute on function api.merge_buckets_numeric(numeric[], numeric[]) to cp_tenant, cp_operator, jobs;
grant execute on function api.fit_buckets(numeric[]) to cp_tenant, cp_operator, jobs;
grant execute on function api.sum_buckets_read(bigint[]) to cp_tenant, cp_operator, jobs;

-- +goose Down
drop aggregate api.sum_buckets_read(bigint[]);
drop function api.fit_buckets(numeric[]);
drop function api.merge_buckets_numeric(numeric[], numeric[]);
drop function api.add_buckets_numeric(numeric[], bigint[]);

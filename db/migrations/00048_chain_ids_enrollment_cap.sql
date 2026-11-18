-- the enrollment answers add their g and c ids to cp.chain_ids (00032) up
-- to the cap the console holds a project to: at most 20 declared ids
-- (control-plane-api 15.3, onchain.MaxIDs). a rejected project that
-- resubmits its enrollment with new ids got them added on top of the ones
-- it already held, so it could hold more than 20, and the console then
-- refused every further add with too_many. the trigger now takes the same
-- per-tenant advisory lock as the console's add, counts the rows the
-- project holds, and adds the new ids in the order the answers list them
-- until the project holds 20; the ids past the cap are left out, as the
-- console would refuse them.

-- +goose Up
-- +goose StatementBegin
create or replace function cp.chain_ids_from_enrollment() returns trigger
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  old_ids jsonb := '[]'::jsonb;
  held int;
begin
  if tg_op = 'UPDATE' then
    old_ids := coalesce(old.enrollment_answers -> 'stellar_ids', '[]'::jsonb);
  end if;
  if jsonb_typeof(coalesce(new.enrollment_answers -> 'stellar_ids', '[]'::jsonb)) <> 'array' then
    return new;
  end if;
  perform pg_advisory_xact_lock(hashtextextended('onchain.ids:' || new.id::text, 0));
  select count(*) into held from cp.chain_ids c where c.tenant_id = new.id;
  insert into cp.chain_ids (tenant_id, strkey, kind, added_by)
  select new.id, x.s, case when x.s like 'G%' then 'account' else 'contract' end, 'enrollment'
  from (
    select e.s, min(e.n) as n
    from jsonb_array_elements_text(coalesce(new.enrollment_answers -> 'stellar_ids', '[]'::jsonb))
         with ordinality as e(s, n)
    where e.s ~ '^[GC][A-Z2-7]{55}$'
      and not (jsonb_typeof(old_ids) = 'array' and old_ids ? e.s)
      and not exists (select 1 from cp.chain_ids c where c.tenant_id = new.id and c.strkey = e.s)
    group by e.s
  ) x
  order by x.n
  limit greatest(20 - held, 0)
  on conflict (tenant_id, strkey) do nothing;
  return new;
end
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
create or replace function cp.chain_ids_from_enrollment() returns trigger
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  old_ids jsonb := '[]'::jsonb;
begin
  if tg_op = 'UPDATE' then
    old_ids := coalesce(old.enrollment_answers -> 'stellar_ids', '[]'::jsonb);
  end if;
  if jsonb_typeof(coalesce(new.enrollment_answers -> 'stellar_ids', '[]'::jsonb)) <> 'array' then
    return new;
  end if;
  insert into cp.chain_ids (tenant_id, strkey, kind, added_by)
  select new.id, s, case when s like 'G%' then 'account' else 'contract' end, 'enrollment'
  from jsonb_array_elements_text(coalesce(new.enrollment_answers -> 'stellar_ids', '[]'::jsonb)) as e(s)
  where s ~ '^[GC][A-Z2-7]{55}$'
    and not (jsonb_typeof(old_ids) = 'array' and old_ids ? s)
  on conflict (tenant_id, strkey) do nothing;
  return new;
end
$$;
-- +goose StatementEnd

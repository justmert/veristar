-- a usage report's revocation is a fact of its own, fixed once written.
-- 00036's guard let revoked_by change on a revoked row, took revoked_at
-- from the caller, and so let a writer back-date or re-attribute a
-- revocation. this version sets revoked_at to the time of the revoking
-- statement for every role and keeps both columns as they are afterwards.
-- the one change it still allows is revoked_by becoming null, which is
-- what deleting the revoking user does through the foreign key.

-- +goose Up
-- +goose StatementBegin
create or replace function cp.usage_report_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if tg_op = 'INSERT' then
    -- nested, so cp.app_user_id() runs only as cp_tenant, the one role
    -- allowed to execute it
    if current_user = 'cp_tenant' then
      if new.created_by is distinct from cp.app_user_id() or new.revoked_at is not null
         or new.view_count <> 0 or new.last_viewed_at is not null then
        raise exception 'cp_tenant may only store a new report by the session user'
          using errcode = 'insufficient_privilege';
      end if;
    end if;
    -- no report is stored already revoked
    if new.revoked_at is not null or new.revoked_by is not null then
      raise exception 'a new usage report is not revoked'
        using errcode = 'insufficient_privilege';
    end if;
    return new;
  end if;
  if new.id is distinct from old.id or new.tenant_id is distinct from old.tenant_id
     or new.period_from is distinct from old.period_from or new.period_to is distinct from old.period_to
     or new.body is distinct from old.body or new.signature is distinct from old.signature
     or new.signer is distinct from old.signer or new.created_at is distinct from old.created_at
     or (new.created_by is distinct from old.created_by and new.created_by is not null) then
    raise exception 'a usage report is immutable'
      using errcode = 'insufficient_privilege';
  end if;
  if old.revoked_at is not null then
    -- a revoked link stays revoked, at the same time and by the same user;
    -- only the user's deletion clears revoked_by
    if new.revoked_at is distinct from old.revoked_at
       or (new.revoked_by is distinct from old.revoked_by and new.revoked_by is not null) then
      raise exception 'a revoked report link stays revoked'
        using errcode = 'insufficient_privilege';
    end if;
    return new;
  end if;
  if new.revoked_at is null then
    if new.revoked_by is not null then
      raise exception 'only a revocation names who revoked'
        using errcode = 'insufficient_privilege';
    end if;
    return new;
  end if;
  -- a revocation now: the time is the database's, not the caller's
  new.revoked_at := now();
  if current_user = 'cp_tenant' then
    if new.revoked_by is distinct from cp.app_user_id() then
      raise exception 'cp_tenant revocations must name the session user'
        using errcode = 'insufficient_privilege';
    end if;
  end if;
  return new;
end
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
create or replace function cp.usage_report_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if tg_op = 'INSERT' then
    if current_user = 'cp_tenant' then
      if new.created_by is distinct from cp.app_user_id() or new.revoked_at is not null
         or new.view_count <> 0 or new.last_viewed_at is not null then
        raise exception 'cp_tenant may only store a new report by the session user'
          using errcode = 'insufficient_privilege';
      end if;
    end if;
    return new;
  end if;
  if new.id is distinct from old.id or new.tenant_id is distinct from old.tenant_id
     or new.period_from is distinct from old.period_from or new.period_to is distinct from old.period_to
     or new.body is distinct from old.body or new.signature is distinct from old.signature
     or new.signer is distinct from old.signer or new.created_at is distinct from old.created_at
     or (new.created_by is distinct from old.created_by and new.created_by is not null) then
    raise exception 'a usage report is immutable'
      using errcode = 'insufficient_privilege';
  end if;
  if new.revoked_at is distinct from old.revoked_at or new.revoked_by is distinct from old.revoked_by then
    if old.revoked_at is not null and new.revoked_at is distinct from old.revoked_at then
      raise exception 'a revoked report link stays revoked'
        using errcode = 'insufficient_privilege';
    end if;
    if current_user = 'cp_tenant' then
      if new.revoked_by is distinct from cp.app_user_id() then
        raise exception 'cp_tenant revocations must name the session user'
          using errcode = 'insufficient_privilege';
      end if;
    end if;
  end if;
  return new;
end
$$;
-- +goose StatementEnd

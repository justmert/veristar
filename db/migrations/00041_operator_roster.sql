-- the operator roster and the operators' passkeys (control-plane-api
-- section 5.1).
--
-- cp.operators holds one row per operator identity: a github account id or
-- a stellar address, never both. the address is a wallet's G-address or a
-- passkey account's C-address (00037), the two forms
-- OPERATOR_STELLAR_ADDRESSES takes, with a role (operator or operator_admin)
-- and a status (active or suspended). operator admins manage the rows in
-- the console; OPERATOR_GITHUB_IDS and OPERATOR_STELLAR_ADDRESSES only
-- ensure, at every start, that the listed identities are active operator
-- admins.
--
-- cp.users.is_operator is derived from the roster and never written on its
-- own: a before trigger on cp.users computes it from the row's identities,
-- and an after trigger on cp.operators recomputes it for every account that
-- holds a changed identity. the update of is_operator fires
-- users_mcp_revoke (00030), so a suspended or removed operator's mcp
-- operator credentials end in the same transaction.
--
-- operators_last_admin refuses any change that leaves the roster without an
-- active operator admin, whatever statement makes it.
--
-- enroll_opened_at and enroll_until bound the window in which the person
-- behind a row may register a first passkey without a step-up (stepup
-- package). a row added by an admin, a bootstrap row whose person has no
-- passkey, and a row whose passkeys an admin reset get a window; the first
-- registration closes it.
--
-- cp.operator_passkeys holds the passkeys an operator account adds for the
-- step-up beside its sign-in passkey (00037): the base64url credential id
-- and the 65-byte sec-1 p-256 key, the same two values cp.users keeps for a
-- sign-in passkey, checked with the same package (internal/controlplane/
-- passkey). no signature counter is kept: synced passkeys report 0.

-- +goose Up
create table cp.operators (
  id               uuid primary key default uuidv7(),
  github_id        bigint check (github_id > 0),
  github_login     text check (octet_length(github_login) between 1 and 64),
  stellar_address  text check (stellar_address ~ '^[GC][A-Z2-7]{55}$'),
  role             text not null check (role in ('operator', 'operator_admin')),
  status           text not null default 'active' check (status in ('active', 'suspended')),
  status_reason    text check (octet_length(status_reason) between 1 and 500),
  enroll_opened_at timestamptz,
  enroll_until     timestamptz,
  created_by       uuid references cp.users (id) on delete set null,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  constraint operators_one_identity check ((github_id is null) <> (stellar_address is null)),
  constraint operators_login_needs_id check (github_login is null or github_id is not null),
  constraint operators_enroll_window check (
    (enroll_opened_at is null) = (enroll_until is null) and (enroll_until is null or enroll_until > enroll_opened_at))
);
create unique index operators_github_id_idx on cp.operators (github_id) where github_id is not null;
create unique index operators_stellar_address_idx on cp.operators (stellar_address) where stellar_address is not null;

create table cp.operator_passkeys (
  id            uuid primary key default uuidv7(),
  user_id       uuid not null references cp.users (id) on delete cascade,
  credential_id text not null unique
                check (credential_id ~ '^[A-Za-z0-9_-]+$' and length(credential_id) <= 1364),
  public_key    bytea not null check (octet_length(public_key) = 65 and get_byte(public_key, 0) = 4),
  name          text not null check (octet_length(name) between 1 and 64),
  created_at    timestamptz not null default now(),
  last_used_at  timestamptz
);
create index operator_passkeys_user_idx on cp.operator_passkeys (user_id);

-- +goose StatementBegin
-- whether an account with these identities is an active operator. security
-- definer, so a migration that updates cp.users as platform_owner computes
-- the same flag as the control plane does
create function cp.roster_active(gh bigint, addr text, pk text) returns boolean
language sql stable security definer
set search_path = pg_catalog, pg_temp
as $$
  select exists (
    select 1 from cp.operators o
    where o.status = 'active'
      and ((gh is not null and o.github_id = gh)
        or (addr is not null and o.stellar_address = addr)
        or (pk is not null and o.stellar_address = pk)))
$$;
-- +goose StatementEnd
revoke all on function cp.roster_active(bigint, text, text) from public;
grant execute on function cp.roster_active(bigint, text, text) to cp_system;

-- +goose StatementBegin
create function cp.users_operator_flag() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  new.is_operator := new.deleted_at is null
    and cp.roster_active(new.github_id, new.stellar_address, new.passkey_address);
  return new;
end
$$;
-- +goose StatementEnd
create trigger users_operator_flag before insert or update of github_id, stellar_address, passkey_address,
  is_operator, deleted_at
  on cp.users for each row execute function cp.users_operator_flag();

-- +goose StatementBegin
-- recomputes the flag of every live account holding an identity of the
-- changed row, before and after the change. it runs with the rights of
-- the role that changed the roster (cp_system), whose policies reach
-- cp.users; platform_owner has none there under forced row-level security
create function cp.operators_sync_flags() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
declare
  ghs bigint[] := array[]::bigint[];
  addrs text[] := array[]::text[];
begin
  if tg_op in ('UPDATE', 'DELETE') then
    ghs := ghs || old.github_id;
    addrs := addrs || old.stellar_address;
  end if;
  if tg_op in ('INSERT', 'UPDATE') then
    ghs := ghs || new.github_id;
    addrs := addrs || new.stellar_address;
  end if;
  update cp.users u
  set is_operator = cp.roster_active(u.github_id, u.stellar_address, u.passkey_address)
  where u.deleted_at is null
    and (u.github_id = any(ghs) or u.stellar_address = any(addrs) or u.passkey_address = any(addrs))
    and u.is_operator is distinct from cp.roster_active(u.github_id, u.stellar_address, u.passkey_address);
  return null;
end
$$;
-- +goose StatementEnd
create trigger operators_sync_flags after insert or update or delete on cp.operators
  for each row execute function cp.operators_sync_flags();

-- +goose StatementBegin
-- a statement that changed no row (an update that matched nothing, for
-- example) changes nothing the rule guards, so it is not checked
create function cp.operators_last_admin() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if exists (select 1 from changed)
     and not exists (select 1 from cp.operators where role = 'operator_admin' and status = 'active') then
    raise exception 'the roster must keep at least one active operator admin'
      using errcode = 'integrity_constraint_violation', constraint = 'operators_last_admin';
  end if;
  return null;
end
$$;
-- +goose StatementEnd
-- a trigger with a transition table names one event, so update and delete
-- get one each
create trigger operators_last_admin_update after update on cp.operators
  referencing old table as changed
  for each statement execute function cp.operators_last_admin();
create trigger operators_last_admin_delete after delete on cp.operators
  referencing old table as changed
  for each statement execute function cp.operators_last_admin();

-- 00030 fired the mcp revoke only on an update that names is_operator.
-- the flag now also changes in users_operator_flag when an identity is
-- linked or unlinked, so the revoke fires on every update that turns it off
drop trigger users_mcp_revoke on cp.users;
create trigger users_mcp_revoke after update on cp.users
  for each row when (old.is_operator and not new.is_operator)
  execute function cp.mcp_revoke_operator();

alter table cp.operators enable row level security;
alter table cp.operators force row level security;
alter table cp.operator_passkeys enable row level security;
alter table cp.operator_passkeys force row level security;

-- the roster and the passkeys are read and written by the control plane's
-- system role only: the guard, the step-up, the operators page and the
-- startup bootstrap all run there. platform_owner reads the roster so the
-- security definer functions above see its rows under forced row-level
-- security.
create policy operators_system on cp.operators for all to cp_system using (true) with check (true);
create policy operators_owner_read on cp.operators for select to platform_owner using (true);
create policy operator_passkeys_system on cp.operator_passkeys for all to cp_system using (true) with check (true);

revoke all on cp.operators, cp.operator_passkeys from public;
grant select, insert, update, delete on cp.operators, cp.operator_passkeys to cp_system;

-- the flags of existing accounts follow the empty roster until the first
-- start of the control plane bootstraps it; that start recomputes them
-- +goose Down
drop trigger users_mcp_revoke on cp.users;
create trigger users_mcp_revoke after update of is_operator on cp.users
  for each row execute function cp.mcp_revoke_operator();
drop trigger operators_last_admin_delete on cp.operators;
drop trigger operators_last_admin_update on cp.operators;
drop function cp.operators_last_admin();
drop trigger operators_sync_flags on cp.operators;
drop function cp.operators_sync_flags();
drop trigger users_operator_flag on cp.users;
drop function cp.users_operator_flag();
drop function cp.roster_active(bigint, text, text);
drop table cp.operator_passkeys;
drop table cp.operators;

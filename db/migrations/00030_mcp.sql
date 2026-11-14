-- the model context protocol server of the control plane (contract section
-- 12, docs/guides/mcp.mdx). ai clients read a project's analytics, or the
-- operators' global view, with a credential that acts as one console user:
--
-- * an mcp access key (kind 'key'): sa_mcp_ plus 40 random characters,
--   stored as its sha-256, made in the console and shown once;
-- * an oauth grant (kind 'oauth'): one consent of the mcp authorization
--   flow, with its short-lived access tokens and rotating refresh tokens in
--   cp.mcp_oauth_tokens.
--
-- both are rows of cp.mcp_credentials with the same scope rules, so one set
-- of revocation triggers covers both: a credential stops working when its
-- member leaves the tenant, when the tenant is suspended, rejected or
-- removed, when an operator credential's user loses the operator flag, and
-- when the user row is deleted (cascade). the control plane also checks
-- membership, tenant status and the operator flag on every request, so the
-- triggers record what the request checks already enforce.

-- +goose Up

-- dynamically registered oauth clients (rfc 7591). public clients only:
-- every client proves possession with pkce, none holds a secret.
create table cp.mcp_oauth_clients (
  id            text primary key check (id ~ '^mcpc_[0-9A-Za-z]{32}$'),
  client_name   text not null check (client_name <> '' and char_length(client_name) <= 100),
  redirect_uris text[] not null check (cardinality(redirect_uris) between 1 and 10),
  created_at    timestamptz not null default now(),
  last_used_at  timestamptz
);

create table cp.mcp_credentials (
  id             uuid primary key default uuidv7(),
  kind           text not null check (kind in ('key', 'oauth')),
  scope          text not null check (scope in ('tenant', 'operator')),
  tenant_id      uuid references cp.tenants (id),
  -- the user the credential acts as; every request reads that user's
  -- membership and operator flag again
  user_id        uuid not null references cp.users (id) on delete cascade,
  name           text not null check (name <> '' and char_length(name) <= 100),
  prefix         text check (prefix <> ''),
  hash           bytea unique check (octet_length(hash) = 32),
  client_id      text references cp.mcp_oauth_clients (id),
  -- the mcp resource uri the credential was issued for (rfc 8707 audience);
  -- a credential is accepted only by a server with that resource uri
  resource       text not null check (resource <> ''),
  created_at     timestamptz not null default now(),
  expires_at     timestamptz,
  revoked_at     timestamptz,
  revoked_reason text check (revoked_reason in ('user', 'operator', 'member_removed', 'tenant_suspended',
                                                'tenant_rejected', 'tenant_removed', 'operator_removed',
                                                'refresh_reuse', 'code_reuse')),
  last_seen_at   timestamptz,
  check ((scope = 'tenant') = (tenant_id is not null)),
  check ((kind = 'key') = (hash is not null and prefix is not null and expires_at is not null)),
  check ((kind = 'oauth') = (client_id is not null)),
  check ((revoked_at is null) = (revoked_reason is null)),
  check (expires_at is null or expires_at > created_at)
);
create index mcp_credentials_tenant_idx on cp.mcp_credentials (tenant_id, created_at);
create index mcp_credentials_user_idx on cp.mcp_credentials (user_id);
create index mcp_credentials_client_idx on cp.mcp_credentials (client_id);

-- authorization codes between consent and the token request: single use,
-- 60 seconds, bound to the client, the redirect uri, the pkce challenge
-- and the resource
create table cp.mcp_oauth_codes (
  hash           bytea primary key check (octet_length(hash) = 32),
  client_id      text not null references cp.mcp_oauth_clients (id) on delete cascade,
  user_id        uuid not null references cp.users (id) on delete cascade,
  scope          text not null check (scope in ('tenant', 'operator')),
  tenant_id      uuid references cp.tenants (id),
  redirect_uri   text not null check (redirect_uri <> ''),
  code_challenge text not null check (code_challenge ~ '^[A-Za-z0-9_-]{43}$'),
  resource       text not null check (resource <> ''),
  created_at     timestamptz not null default now(),
  expires_at     timestamptz not null,
  used_at        timestamptz,
  -- the grant the code was exchanged for: a second use of the code revokes
  -- it (rfc 6749 section 4.1.2)
  credential_id  uuid references cp.mcp_credentials (id) on delete set null,
  check ((scope = 'tenant') = (tenant_id is not null))
);
create index mcp_oauth_codes_expires_idx on cp.mcp_oauth_codes (expires_at);

-- access and refresh tokens of an oauth grant, as sha-256. a refresh token
-- is used once (rotation); presenting a used one revokes the whole grant
create table cp.mcp_oauth_tokens (
  hash          bytea primary key check (octet_length(hash) = 32),
  credential_id uuid not null references cp.mcp_credentials (id) on delete cascade,
  kind          text not null check (kind in ('access', 'refresh')),
  created_at    timestamptz not null default now(),
  expires_at    timestamptz not null,
  used_at       timestamptz,
  check (used_at is null or kind = 'refresh')
);
create index mcp_oauth_tokens_credential_idx on cp.mcp_oauth_tokens (credential_id);
create index mcp_oauth_tokens_expires_idx on cp.mcp_oauth_tokens (expires_at);

-- row-level security: both tables with a tenant_id follow the rules of 00003
alter table cp.mcp_credentials enable row level security;
alter table cp.mcp_credentials force row level security;
alter table cp.mcp_oauth_codes enable row level security;
alter table cp.mcp_oauth_codes force row level security;

create policy mcp_credentials_tenant on cp.mcp_credentials for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy mcp_credentials_system on cp.mcp_credentials for all to cp_system using (true) with check (true);
-- the revocation triggers below run with platform_owner's rights
create policy mcp_credentials_revoke_read on cp.mcp_credentials for select to platform_owner using (true);
create policy mcp_credentials_revoke on cp.mcp_credentials for update to platform_owner using (true) with check (true);

-- cp_tenant gets no right on codes; the policy is the one 00003 requires
create policy mcp_oauth_codes_tenant on cp.mcp_oauth_codes for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy mcp_oauth_codes_system on cp.mcp_oauth_codes for all to cp_system using (true) with check (true);

revoke all on cp.mcp_oauth_clients, cp.mcp_credentials, cp.mcp_oauth_codes, cp.mcp_oauth_tokens from public;
grant select, insert, update, delete on cp.mcp_oauth_clients, cp.mcp_credentials, cp.mcp_oauth_codes,
  cp.mcp_oauth_tokens to cp_system;
-- the console's key routes for one tenant: list, create, revoke
grant select, insert on cp.mcp_credentials to cp_tenant;
grant update (revoked_at, revoked_reason) on cp.mcp_credentials to cp_tenant;

-- cp_tenant may create only access keys of its tenant that act as the
-- session user, and may only revoke (never restore or edit) a credential.
-- the trigger tests current_user, the login role of the pool, as 00024 does.
-- +goose StatementBegin
create function cp.tenant_mcp_credential_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if current_user <> 'cp_tenant' then
    return new;
  end if;
  if tg_op = 'INSERT' then
    if new.kind <> 'key' or new.scope <> 'tenant' or new.user_id is distinct from cp.app_user_id()
       or new.revoked_at is not null then
      raise exception 'cp_tenant may only create tenant access keys for the session user'
        using errcode = 'insufficient_privilege';
    end if;
    return new;
  end if;
  if old.revoked_at is not null or new.revoked_at is null or new.revoked_reason <> 'user' then
    raise exception 'cp_tenant may only revoke an active mcp credential'
      using errcode = 'insufficient_privilege';
  end if;
  return new;
end
$$;
-- +goose StatementEnd
create trigger mcp_credentials_tenant_guard before insert or update on cp.mcp_credentials
  for each row execute function cp.tenant_mcp_credential_guard();
revoke all on function cp.tenant_mcp_credential_guard() from public;

-- revokes the active credentials a change ends and writes one audit row per
-- credential, in the transaction of the change. security definer, because
-- the change can come from cp_tenant (a member removed), which has no right
-- to another user's credential.
-- +goose StatementBegin
create function cp.mcp_revoke_matching(match_tenant uuid, match_user uuid, match_scope text, reason text) returns void
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  c record;
begin
  for c in
    update cp.mcp_credentials m
    set revoked_at = now(), revoked_reason = reason
    where m.revoked_at is null
      and (match_tenant is null or m.tenant_id = match_tenant)
      and (match_user is null or m.user_id = match_user)
      and (match_scope is null or m.scope = match_scope)
    returning m.id, m.kind, m.scope, m.tenant_id, m.user_id
  loop
    insert into cp.audit_log (actor, action, target, detail)
    values ('system:mcp-revoke', 'mcp_credential.revoke', 'mcp_credential:' || c.id::text,
            jsonb_build_object('tenant_id', c.tenant_id, 'user_id', c.user_id, 'kind', c.kind,
                               'scope', c.scope, 'reason', reason));
  end loop;
end
$$;
-- +goose StatementEnd
revoke all on function cp.mcp_revoke_matching(uuid, uuid, text, text) from public;

-- +goose StatementBegin
create function cp.mcp_revoke_member() returns trigger
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
begin
  perform cp.mcp_revoke_matching(old.tenant_id, old.user_id, 'tenant', 'member_removed');
  return old;
end
$$;
-- +goose StatementEnd
create trigger tenant_members_mcp_revoke after delete on cp.tenant_members
  for each row execute function cp.mcp_revoke_member();
revoke all on function cp.mcp_revoke_member() from public;

-- +goose StatementBegin
create function cp.mcp_revoke_tenant() returns trigger
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
begin
  if new.status in ('suspended', 'rejected', 'removed') and new.status is distinct from old.status then
    perform cp.mcp_revoke_matching(new.id, null, 'tenant', 'tenant_' || new.status);
  end if;
  return new;
end
$$;
-- +goose StatementEnd
create trigger tenants_mcp_revoke after update of status on cp.tenants
  for each row execute function cp.mcp_revoke_tenant();
revoke all on function cp.mcp_revoke_tenant() from public;

-- +goose StatementBegin
create function cp.mcp_revoke_operator() returns trigger
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
begin
  if old.is_operator and not new.is_operator then
    perform cp.mcp_revoke_matching(null, new.id, 'operator', 'operator_removed');
  end if;
  return new;
end
$$;
-- +goose StatementEnd
create trigger users_mcp_revoke after update of is_operator on cp.users
  for each row execute function cp.mcp_revoke_operator();
revoke all on function cp.mcp_revoke_operator() from public;

-- +goose Down
drop trigger users_mcp_revoke on cp.users;
drop function cp.mcp_revoke_operator();
drop trigger tenants_mcp_revoke on cp.tenants;
drop function cp.mcp_revoke_tenant();
drop trigger tenant_members_mcp_revoke on cp.tenant_members;
drop function cp.mcp_revoke_member();
drop function cp.mcp_revoke_matching(uuid, uuid, text, text);
drop trigger mcp_credentials_tenant_guard on cp.mcp_credentials;
drop function cp.tenant_mcp_credential_guard();
drop table cp.mcp_oauth_tokens;
drop table cp.mcp_oauth_codes;
drop table cp.mcp_credentials;
drop table cp.mcp_oauth_clients;

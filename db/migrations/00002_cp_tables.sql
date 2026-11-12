-- control plane tables (the design). rights and row-level security follow
-- in 00003, so that every grant sits in one file that can be read as a whole.

-- +goose Up

-- console identity is github only: no email, no password
create table cp.users (
  id            uuid primary key default uuidv7(),
  github_id     bigint not null unique check (github_id > 0),
  github_login  text not null check (github_login <> ''),
  is_operator   boolean not null default false,
  created_at    timestamptz not null default now(),
  last_login_at timestamptz
);

-- console sessions. only a hash of the cookie value is stored, so a database
-- read does not hand out live sessions.
create table cp.sessions (
  token_hash bytea primary key check (octet_length(token_hash) = 32),
  user_id    uuid references cp.users (id) on delete cascade,
  data       bytea not null,
  expiry     timestamptz not null
);
create index sessions_expiry_idx on cp.sessions (expiry);
create index sessions_user_idx on cp.sessions (user_id);

create table cp.tenants (
  id                     uuid primary key default uuidv7(),
  slug                   text not null unique check (slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
  name                   text not null check (name <> ''),
  description            text not null default '',
  status                 text not null default 'pending'
                         check (status in ('pending', 'active', 'rejected', 'suspended', 'removed')),
  -- the monitoring probe tenant (this stage), hidden from operator views
  is_internal            boolean not null default false,
  enrollment_answers     jsonb not null default '{}'::jsonb check (jsonb_typeof(enrollment_answers) = 'object'),
  accepted_terms_version text,
  umami_team_id          uuid unique,
  created_by             uuid references cp.users (id),
  approved_by            uuid references cp.users (id),
  status_reason          text,
  created_at             timestamptz not null default now(),
  updated_at             timestamptz not null default now(),
  status_changed_at      timestamptz not null default now()
);

create table cp.tenant_members (
  tenant_id  uuid not null references cp.tenants (id),
  user_id    uuid not null references cp.users (id),
  role       text not null check (role in ('owner', 'member')),
  created_at timestamptz not null default now(),
  primary key (tenant_id, user_id)
);
create index tenant_members_user_idx on cp.tenant_members (user_id);

create table cp.invites (
  id           uuid primary key default uuidv7(),
  tenant_id    uuid not null references cp.tenants (id),
  github_login text not null check (github_login <> ''),
  role         text not null check (role in ('owner', 'member')),
  created_by   uuid not null references cp.users (id),
  created_at   timestamptz not null default now(),
  expires_at   timestamptz not null
);
-- github logins are case-insensitive, so one open invite per login and tenant
create unique index invites_tenant_login_idx on cp.invites (tenant_id, lower(github_login));
create index invites_login_idx on cp.invites (lower(github_login));

-- id equals the primary umami website id
create table cp.sites (
  id                      uuid primary key,
  tenant_id               uuid not null references cp.tenants (id),
  name                    text not null check (name <> ''),
  domain                  text not null check (domain <> ''),
  origins                 text[] not null default '{}',
  quarantine_website_id   uuid unique,
  status                  text not null default 'pending'
                          check (status in ('pending', 'active', 'suspended', 'removed')),
  verify_token            text not null,
  verify_method           text check (verify_method in ('dns', 'file')),
  verified_at             timestamptz,
  last_verify_at          timestamptz,
  last_verify_error       text,
  stellar_toml            jsonb,
  stellar_toml_checked_at timestamptz,
  created_at              timestamptz not null default now(),
  updated_at              timestamptz not null default now(),
  check (quarantine_website_id is null or quarantine_website_id <> id)
);
create index sites_tenant_idx on cp.sites (tenant_id);

-- otlp bearer tokens: sha-256 of the token, prefix for display
create table cp.otlp_tokens (
  id           uuid primary key default uuidv7(),
  tenant_id    uuid not null references cp.tenants (id),
  name         text not null check (name <> ''),
  environment  text not null default '',
  prefix       text not null check (prefix <> ''),
  hash         bytea not null unique check (octet_length(hash) = 32),
  status       text not null default 'active' check (status in ('active', 'revoked')),
  created_by   uuid not null references cp.users (id),
  created_at   timestamptz not null default now(),
  revoked_at   timestamptz,
  last_seen_at timestamptz,
  check ((status = 'revoked') = (revoked_at is not null))
);
create index otlp_tokens_tenant_idx on cp.otlp_tokens (tenant_id);

-- operator machine api keys. scope_tenant_id narrows a key to one
-- tenant; it is not a tenant_id column because the key belongs to operators.
create table cp.operator_api_keys (
  id              uuid primary key default uuidv7(),
  name            text not null check (name <> ''),
  prefix          text not null check (prefix <> ''),
  hash            bytea not null unique check (octet_length(hash) = 32),
  scope           text not null check (scope in ('global', 'tenant')),
  scope_tenant_id uuid references cp.tenants (id),
  expires_at      timestamptz not null,
  revoked_at      timestamptz,
  created_by      uuid not null references cp.users (id),
  created_at      timestamptz not null default now(),
  last_used_at    timestamptz,
  check ((scope = 'tenant') = (scope_tenant_id is not null)),
  check (expires_at > created_at)
);

create table cp.api_key_usage_hourly (
  key_id   uuid not null references cp.operator_api_keys (id),
  hour     timestamptz not null check (date_trunc('hour', hour) = hour),
  requests bigint not null default 0 check (requests >= 0),
  rejected bigint not null default 0 check (rejected >= 0),
  primary key (key_id, hour)
);

create table cp.settings (
  key   text primary key check (key <> ''),
  value jsonb not null
);

-- append-only: see the triggers and grants in 00003
create table cp.audit_log (
  id     bigint generated always as identity primary key,
  at     timestamptz not null default now(),
  actor  text not null check (actor <> ''),
  action text not null check (action <> ''),
  target text not null default '',
  detail jsonb not null default '{}'::jsonb
);
create index audit_log_at_idx on cp.audit_log (at);

-- never holds an ip address or user agent (the design)
create table cp.erasure_requests (
  id                   uuid primary key default uuidv7(),
  tenant_id            uuid not null references cp.tenants (id),
  site_id              uuid not null references cp.sites (id),
  kind                 text not null check (kind in ('visitor_today', 'session', 'site_reset')),
  status               text not null default 'pending' check (status in ('pending', 'running', 'done', 'failed')),
  requested_by         uuid not null references cp.users (id),
  requested_at         timestamptz not null default now(),
  completed_at         timestamptz,
  sessions_deleted     bigint not null default 0 check (sessions_deleted >= 0),
  events_deleted       bigint not null default 0 check (events_deleted >= 0),
  dlq_messages_deleted bigint not null default 0 check (dlq_messages_deleted >= 0),
  error                text
);
create index erasure_requests_tenant_idx on cp.erasure_requests (tenant_id);

-- per-site outcome counters from EDGE_STATS. no foreign key to sites, because
-- aggregates may outlive a removed site (setting removal_keep_aggregates).
create table cp.web_ingest_hourly (
  site_id   uuid not null,
  tenant_id uuid not null,
  hour      timestamptz not null check (date_trunc('hour', hour) = hour),
  outcome   text not null check (outcome <> ''),
  count     bigint not null default 0 check (count >= 0),
  primary key (site_id, hour, outcome)
);
create index web_ingest_hourly_tenant_idx on cp.web_ingest_hourly (tenant_id, hour);

-- EDGE_STATS message ids already applied, so a redelivered message is not
-- counted twice
create table cp.stats_dedup (
  message_id text primary key check (message_id <> ''),
  applied_at timestamptz not null default now()
);

-- fencing tokens for singleton workers. the revision is the nats kv
-- revision of the worker's leader lock.
create table cp.leader_fence (
  name     text primary key check (name <> ''),
  revision bigint not null default 0 check (revision >= 0)
);

-- +goose Down
drop table cp.leader_fence;
drop table cp.stats_dedup;
drop table cp.web_ingest_hourly;
drop table cp.erasure_requests;
drop table cp.audit_log;
drop table cp.settings;
drop table cp.api_key_usage_hourly;
drop table cp.operator_api_keys;
drop table cp.otlp_tokens;
drop table cp.sites;
drop table cp.invites;
drop table cp.tenant_members;
drop table cp.tenants;
drop table cp.sessions;
drop table cp.users;

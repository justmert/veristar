-- removal progress of a tenant (control-plane-api 5.5). the
-- operator's remove action inserts the row in the same transaction as the
-- status change; the purge runner in the reconciler fills steps store by
-- store, so a crash or a failing store resumes where it stopped, and a store
-- added in a later phase runs for tenants removed before it existed.

-- +goose Up
create table cp.tenant_removals (
  tenant_id       uuid primary key references cp.tenants (id),
  requested_at    timestamptz not null default now(),
  requested_by    uuid references cp.users (id),
  reason          text not null check (reason <> ''),
  -- copied from cp.settings.removal_keep_aggregates at removal time, so a
  -- later change of the setting never changes a removal in progress
  keep_aggregates boolean not null,
  -- {"<purger name>": {"done_at": "...", "detail": {...}}}
  steps           jsonb not null default '{}'::jsonb check (jsonb_typeof(steps) = 'object'),
  completed_at    timestamptz
);

-- a tenant_id column means the coverage rules of 00003 apply: forced
-- row-level security and exactly one cp_tenant policy, although cp_tenant
-- gets no right on the table
alter table cp.tenant_removals enable row level security;
alter table cp.tenant_removals force row level security;
create policy tenant_removals_tenant on cp.tenant_removals for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy tenant_removals_system on cp.tenant_removals for all to cp_system using (true) with check (true);
create policy tenant_removals_operator on cp.tenant_removals for select to cp_operator using (true);

revoke all on cp.tenant_removals from public;
grant select, insert, update, delete on cp.tenant_removals to cp_system;
grant select on cp.tenant_removals to cp_operator;

-- +goose Down
drop table cp.tenant_removals;

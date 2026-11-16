-- cohorts and saved threshold rules of the operator views
-- (control-plane-api section 19).
--
-- cp.tenant_cohorts holds one row per project: the program, round, category
-- and award date operators compare it by, and beside them what the project
-- proposed. scf publishes no machine-readable directory of its awards, so
-- the values come from two sides: a project proposes them at enrollment or
-- later in the console (the proposed_* columns, written under cp_tenant
-- through column grants), and an operator confirms the proposal or sets the
-- values directly (the confirmed columns, written under cp_system with an
-- audit row in the same transaction). only confirmed values place a
-- project in a cohort; a proposal is shown beside them until it is
-- confirmed or replaced.
--
-- cp.threshold_rules holds the operators' saved rules: a metric, a daily
-- minimum, a number of qualifying days per calendar month, a number of
-- months, an optional minimum integrity grade and an optional cohort. the
-- rows hold no tenant data, so cp_tenant has no right on them; a project
-- reads the rules it meets through the "what scf sees" route, which runs
-- under cp_operator.
--
-- rules of 00003 apply: forced row-level security and exactly one
-- cp_tenant policy keyed on cp.current_tenant_id() on every table with a
-- tenant_id.

-- +goose Up
create table cp.tenant_cohorts (
  tenant_id           uuid primary key references cp.tenants (id) on delete cascade,
  -- confirmed by an operator: these place the project in a cohort
  program             text check (program = btrim(program) and char_length(program) between 1 and 80),
  round               text check (round = btrim(round) and char_length(round) between 1 and 80),
  category            text check (category = btrim(category) and char_length(category) between 1 and 80),
  award_date          date check (award_date >= date '2018-01-01'),
  confirmed_by        uuid references cp.users (id) on delete set null,
  confirmed_at        timestamptz,
  -- proposed by a member of the project, at enrollment or in the console
  proposed_program    text check (proposed_program = btrim(proposed_program)
                                  and char_length(proposed_program) between 1 and 80),
  proposed_round      text check (proposed_round = btrim(proposed_round)
                                  and char_length(proposed_round) between 1 and 80),
  proposed_category   text check (proposed_category = btrim(proposed_category)
                                  and char_length(proposed_category) between 1 and 80),
  proposed_award_date date check (proposed_award_date >= date '2018-01-01'),
  proposed_by         uuid references cp.users (id) on delete set null,
  proposed_at         timestamptz,
  updated_at          timestamptz not null default now(),
  check ((confirmed_at is null) = (program is null and round is null and category is null and award_date is null)),
  check ((proposed_at is null) = (proposed_program is null and proposed_round is null
                                  and proposed_category is null and proposed_award_date is null))
);
create index tenant_cohorts_cohort_idx on cp.tenant_cohorts (program, round, category);

create table cp.threshold_rules (
  id          uuid primary key default uuidv7(),
  name        text not null check (name = btrim(name) and char_length(name) between 1 and 100),
  metric      text not null check (metric in ('visitors', 'requests', 'onchain_operations')),
  min_per_day bigint not null check (min_per_day between 1 and 1000000000000),
  min_days    integer not null check (min_days between 1 and 28),
  months      integer not null check (months between 1 and 12),
  min_grade   text check (min_grade in ('A', 'B', 'C')),
  -- an optional cohort: only projects confirmed in it are evaluated
  program     text check (program = btrim(program) and char_length(program) between 1 and 80),
  round       text check (round = btrim(round) and char_length(round) between 1 and 80),
  category    text check (category = btrim(category) and char_length(category) between 1 and 80),
  created_by  uuid references cp.users (id) on delete set null,
  created_at  timestamptz not null default now(),
  updated_by  uuid references cp.users (id) on delete set null,
  updated_at  timestamptz not null default now()
);

alter table cp.tenant_cohorts enable row level security;
alter table cp.tenant_cohorts force row level security;
alter table cp.threshold_rules enable row level security;
alter table cp.threshold_rules force row level security;

-- a member reads its own project's row and writes only the proposal
create policy tenant_cohorts_tenant on cp.tenant_cohorts for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy tenant_cohorts_system on cp.tenant_cohorts for all to cp_system using (true) with check (true);
create policy tenant_cohorts_operator on cp.tenant_cohorts for select to cp_operator using (true);

create policy threshold_rules_system on cp.threshold_rules for all to cp_system using (true) with check (true);
create policy threshold_rules_operator on cp.threshold_rules for select to cp_operator using (true);

revoke all on cp.tenant_cohorts, cp.threshold_rules from public;
grant select, insert, update, delete on cp.tenant_cohorts, cp.threshold_rules to cp_system;
grant select on cp.tenant_cohorts, cp.threshold_rules to cp_operator;
grant select on cp.tenant_cohorts to cp_tenant;
grant insert (tenant_id, proposed_program, proposed_round, proposed_category, proposed_award_date,
              proposed_by, proposed_at, updated_at) on cp.tenant_cohorts to cp_tenant;
grant update (proposed_program, proposed_round, proposed_category, proposed_award_date,
              proposed_by, proposed_at, updated_at) on cp.tenant_cohorts to cp_tenant;

-- +goose StatementBegin
-- a proposal written under cp_tenant always names the session user, so a
-- handler bug cannot attribute a proposal to someone else
create function cp.tenant_cohort_proposal_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if current_user = 'cp_tenant' and new.proposed_by is distinct from cp.app_user_id() then
    raise exception 'cp_tenant proposals must name the session user'
      using errcode = 'insufficient_privilege';
  end if;
  return new;
end
$$;
-- +goose StatementEnd
create trigger tenant_cohorts_proposal_guard before insert or update on cp.tenant_cohorts
  for each row execute function cp.tenant_cohort_proposal_guard();

-- +goose Down
drop trigger tenant_cohorts_proposal_guard on cp.tenant_cohorts;
drop function cp.tenant_cohort_proposal_guard();
drop table cp.threshold_rules;
drop table cp.tenant_cohorts;

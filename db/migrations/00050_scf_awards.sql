-- the scf award directory and the cohort proposals made from it
-- (control-plane-api section 19.4).
--
-- cp.scf_awards holds one row per funded project and round, imported by an
-- operator from an scf build award export with platformctl scf import. it
-- holds organisation data only: the project's name, round, category,
-- program, award date and amount, tranche, website host and the stellar ids
-- the export declares. the import drops every other column of the export
-- (people's names, linkedin and x handles, document and docsend links,
-- descriptions, traction text) while it parses, so none of them reaches the
-- database. the rows have no tenant: they describe scf's funded projects,
-- enrolled or not, so cp_tenant has no right on them and a tenant removal
-- leaves them in place.
--
-- cp.scf_award_imports records each import run: the file's sha-256 and
-- size, the counts and who ran it.
--
-- cp.tenant_cohorts gains where a pending proposal comes from
-- (proposed_source: 'member' or 'scf_import') and the award the import last
-- proposed (scf_award_id), kept after the operator confirms or dismisses
-- the proposal so a later import never proposes the same award again.

-- +goose Up
create table cp.scf_award_imports (
  id          uuid primary key default uuidv7(),
  file_sha256 bytea not null check (octet_length(file_sha256) = 32),
  file_bytes  bigint not null check (file_bytes >= 0),
  has_header  boolean not null,
  delimiter   text not null check (delimiter in ('tab', 'comma')),
  records     integer not null check (records >= 0),
  inserted    integer not null check (inserted >= 0),
  updated     integer not null check (updated >= 0),
  unchanged   integer not null check (unchanged >= 0),
  rejected    integer not null check (rejected >= 0),
  actor       text not null check (actor <> ''),
  imported_at timestamptz not null default now(),
  check (inserted + updated + unchanged + rejected = records)
);

create table cp.scf_awards (
  id                uuid primary key default uuidv7(),
  project           text not null check (project = btrim(project) and char_length(project) between 1 and 200
                                         and project !~ '[[:cntrl:]]'),
  round             text not null check (round ~ '^SCF #[0-9]{1,4}$'),
  category          text not null check (category = btrim(category) and char_length(category) between 1 and 80),
  program           text not null check (program = btrim(program) and char_length(program) between 1 and 80),
  award_date        date check (award_date >= date '2018-01-01'),
  award_amount      numeric(14, 2) not null check (award_amount >= 0),
  tranche_percent   smallint check (tranche_percent between 0 and 100),
  tranche_status    text check (tranche_status = btrim(tranche_status) and char_length(tranche_status) between 1 and 80
                                and tranche_status !~ '[[:cntrl:]]'),
  -- lowercase a-label host without www. and without a trailing dot
  website_host      text check (char_length(website_host) <= 253 and website_host like '%.%'
                                and website_host ~ '^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$'),
  -- account (g) and contract (c) strkeys with a valid checksum
  strkeys           text[] not null default '{}' check (cardinality(strkeys) <= 50
                                and array_to_string(strkeys, ' ') ~ '^([GC][A-Z2-7]{55}( |$))*$'),
  import_id         uuid not null references cp.scf_award_imports (id),
  first_imported_at timestamptz not null default now(),
  imported_at       timestamptz not null default now()
);
-- the import upserts by project and round; a change of letter case in a
-- later export updates the same row
create unique index scf_awards_project_round_idx on cp.scf_awards (lower(project), round);
create index scf_awards_website_host_idx on cp.scf_awards (website_host);

alter table cp.scf_award_imports enable row level security;
alter table cp.scf_award_imports force row level security;
alter table cp.scf_awards enable row level security;
alter table cp.scf_awards force row level security;

create policy scf_award_imports_system on cp.scf_award_imports for all to cp_system using (true) with check (true);
create policy scf_award_imports_operator on cp.scf_award_imports for select to cp_operator using (true);
create policy scf_awards_system on cp.scf_awards for all to cp_system using (true) with check (true);
create policy scf_awards_operator on cp.scf_awards for select to cp_operator using (true);

revoke all on cp.scf_award_imports, cp.scf_awards from public;
grant select, insert on cp.scf_award_imports to cp_system;
grant select, insert, update on cp.scf_awards to cp_system;
grant select on cp.scf_award_imports, cp.scf_awards to cp_operator;

alter table cp.tenant_cohorts
  add column proposed_source text check (proposed_source in ('member', 'scf_import')),
  add column scf_award_id uuid references cp.scf_awards (id);
update cp.tenant_cohorts set proposed_source = 'member' where proposed_at is not null;
alter table cp.tenant_cohorts
  add constraint tenant_cohorts_proposal_source_check check ((proposed_at is null) = (proposed_source is null));
create index tenant_cohorts_scf_award_idx on cp.tenant_cohorts (scf_award_id);

grant insert (proposed_source), update (proposed_source) on cp.tenant_cohorts to cp_tenant;

-- +goose StatementBegin
-- a proposal written under cp_tenant always names the session user and is
-- always the member's own, so neither a handler bug nor a member can pass
-- a proposal off as one of the scf import
create or replace function cp.tenant_cohort_proposal_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  -- nested, so cp.app_user_id() runs only as cp_tenant, the one role
  -- allowed to execute it
  if current_user = 'cp_tenant' then
    if new.proposed_by is distinct from cp.app_user_id() then
      raise exception 'cp_tenant proposals must name the session user'
        using errcode = 'insufficient_privilege';
    end if;
    if new.proposed_source is distinct from 'member' and new.proposed_at is not null then
      raise exception 'cp_tenant proposals are the member''s own'
        using errcode = 'insufficient_privilege';
    end if;
  end if;
  return new;
end
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
create or replace function cp.tenant_cohort_proposal_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if current_user = 'cp_tenant' then
    if new.proposed_by is distinct from cp.app_user_id() then
      raise exception 'cp_tenant proposals must name the session user'
        using errcode = 'insufficient_privilege';
    end if;
  end if;
  return new;
end
$$;
-- +goose StatementEnd
drop index cp.tenant_cohorts_scf_award_idx;
alter table cp.tenant_cohorts drop constraint tenant_cohorts_proposal_source_check;
alter table cp.tenant_cohorts drop column scf_award_id, drop column proposed_source;
drop table cp.scf_awards;
drop table cp.scf_award_imports;

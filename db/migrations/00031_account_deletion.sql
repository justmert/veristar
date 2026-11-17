-- console account deletion (contract 4.1.6). a person deletes their own
-- account; the control plane removes the memberships, sessions, mcp
-- credentials and invites of the account and empties its cp.users row.
--
-- the row itself stays: tenants, invites, sites, tokens, api keys, erasure
-- requests and removals name their creator by user id, and the append-only
-- audit log names actors as 'user:<id>'. keeping the id keeps that history
-- consistent, while the github id and login, the only data that identify
-- the person, are cleared. the remaining id is a random uuidv7 that nothing
-- outside the platform links to a person.
--
-- an emptied row keeps github_id 0 and github_login '' (both columns stay
-- not null, so every query that reads them keeps its type) and has
-- deleted_at set. the unique index on github_id covers live rows only, so
-- any number of emptied rows can hold 0, and a person who signs in again
-- after deleting their account gets a new row with a new id.

-- +goose Up
alter table cp.users add column deleted_at timestamptz;

alter table cp.users drop constraint users_github_id_check;
alter table cp.users drop constraint users_github_login_check;
alter table cp.users drop constraint users_github_id_key;
create unique index users_github_id_live_idx on cp.users (github_id) where deleted_at is null;

-- a live row names a github account; an emptied row names nothing and can
-- hold no operator flag and no sign-in time
alter table cp.users add constraint users_identity_check check (
  (deleted_at is null and github_id > 0 and github_login <> '')
  or (deleted_at is not null and github_id = 0 and github_login = ''
      and not is_operator and last_login_at is null)
);

-- +goose Down
-- emptied rows cannot satisfy the old constraints, so the down step refuses
-- while any exists instead of inventing github ids for them. the down step
-- runs as platform_owner, which cp.users' forced row-level security gives no
-- policy, so the check lifts the force; otherwise it would see no rows
alter table cp.users no force row level security;
-- +goose StatementBegin
do $$
begin
  if exists (select 1 from cp.users where deleted_at is not null) then
    raise exception 'cp.users holds deleted accounts; they cannot be restored to the old constraints';
  end if;
end
$$;
-- +goose StatementEnd
alter table cp.users force row level security;
alter table cp.users drop constraint users_identity_check;
drop index cp.users_github_id_live_idx;
alter table cp.users add constraint users_github_id_key unique (github_id);
alter table cp.users add constraint users_github_login_check check (github_login <> '');
alter table cp.users add constraint users_github_id_check check (github_id > 0);
alter table cp.users drop column deleted_at;

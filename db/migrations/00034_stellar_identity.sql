-- wallet sign-in (contract 4.1.7). a console account is a github account, a
-- stellar account (a G-address whose key signed a sep-10 challenge), or
-- both linked. the address is a second identity column on cp.users rather
-- than a table of its own: an account holds at most one of each kind, and
-- every place that names a person (member lists, invites, the audit) reads
-- the one row.
--
-- github_id and github_login become nullable, so a wallet-only account
-- holds no github identity at all. an emptied (deleted) row now holds
-- nulls instead of the 0 and '' that 00031 used, so no column carries a
-- stand-in value.
--
-- an invite names either a github account (by id, 00025) or a stellar
-- account (by address).
--
-- the umami user of an account was named gh-<github id>. a github id can now
-- be linked to an account after its umami user exists, or unlinked, so the
-- name moves to cp.users.umami_username: the reconciler assigns
-- u-<account id> when it first creates the umami user, existing accounts
-- keep the gh-<github id> their umami user already carries, and sso logs in
-- with the stored name. deletion clears it with the rest of the identity.
--
-- cp.wallet_challenges_used makes a signed challenge single use across
-- every control plane instance: the verification inserts the hash of the
-- challenge nonce in the transaction that signs the person in or links the
-- address, and a second insert of the same hash fails. rows live until the
-- challenge's expiry and are pruned by later verifications.

-- +goose Up
alter table cp.users alter column github_id drop not null;
alter table cp.users alter column github_login drop not null;
alter table cp.users add column stellar_address text;
alter table cp.users add column umami_username text unique;

-- the migration runs as platform_owner, which forced row-level security
-- would hide every row from; the force is lifted for this one statement
alter table cp.users no force row level security;
update cp.users set github_id = null, github_login = null where deleted_at is not null;
update cp.users set umami_username = 'gh-' || github_id where deleted_at is null;
alter table cp.users force row level security;

alter table cp.users drop constraint users_identity_check;
-- a live row names a github account, a stellar account or both; the github
-- id and login come together. an emptied row names nothing and can hold no
-- operator flag and no sign-in time. the pattern is the strkey shape of an
-- account id; the control plane checks the checksum before it writes one.
alter table cp.users add constraint users_identity_check check (
  (deleted_at is null
    and (github_id is null) = (github_login is null)
    and (github_id is null or (github_id > 0 and github_login <> ''))
    and (stellar_address is null or stellar_address ~ '^G[A-Z2-7]{55}$')
    and (github_id is not null or stellar_address is not null))
  or (deleted_at is not null
    and github_id is null and github_login is null and stellar_address is null
    and umami_username is null and not is_operator and last_login_at is null)
);
create unique index users_stellar_address_live_idx on cp.users (stellar_address)
  where deleted_at is null and stellar_address is not null;

-- member lists show the address of a wallet member
grant select (stellar_address) on cp.users to cp_tenant;

alter table cp.invites alter column github_id drop not null;
alter table cp.invites alter column github_login drop not null;
alter table cp.invites add column stellar_address text
  check (stellar_address ~ '^G[A-Z2-7]{55}$');
alter table cp.invites add constraint invites_identity_check check (
  (github_id is not null and github_login is not null and stellar_address is null)
  or (github_id is null and github_login is null and stellar_address is not null)
);
-- one open invite per address and tenant, as for github ids
create unique index invites_tenant_stellar_idx on cp.invites (tenant_id, stellar_address)
  where stellar_address is not null;
create index invites_stellar_idx on cp.invites (stellar_address) where stellar_address is not null;

create table cp.wallet_challenges_used (
  -- sha-256 of the challenge nonce; the nonce itself is not kept
  nonce_hash bytea primary key check (octet_length(nonce_hash) = 32),
  expires_at timestamptz not null
);
create index wallet_challenges_used_expires_idx on cp.wallet_challenges_used (expires_at);
alter table cp.wallet_challenges_used enable row level security;
alter table cp.wallet_challenges_used force row level security;
create policy wallet_challenges_used_system on cp.wallet_challenges_used for all to cp_system
  using (true) with check (true);
revoke all on cp.wallet_challenges_used from public;
grant select, insert, delete on cp.wallet_challenges_used to cp_system;

-- +goose Down
-- wallet accounts and wallet invites cannot be expressed in the old
-- columns, so the down step refuses while any exists
-- +goose StatementBegin
do $$
begin
  if exists (select 1 from cp.users where stellar_address is not null and github_id is null and deleted_at is null) then
    raise exception 'cp.users holds wallet-only accounts; they cannot be restored to the old constraints';
  end if;
  if exists (select 1 from cp.invites where stellar_address is not null) then
    raise exception 'cp.invites holds wallet invites; they cannot be restored to the old constraints';
  end if;
end
$$;
-- +goose StatementEnd
drop table cp.wallet_challenges_used;
drop index cp.invites_stellar_idx;
drop index cp.invites_tenant_stellar_idx;
alter table cp.invites drop constraint invites_identity_check;
alter table cp.invites drop column stellar_address;
alter table cp.invites alter column github_login set not null;
alter table cp.invites alter column github_id set not null;
revoke select (stellar_address) on cp.users from cp_tenant;
drop index cp.users_stellar_address_live_idx;
alter table cp.users drop constraint users_identity_check;
alter table cp.users no force row level security;
update cp.users set github_id = 0, github_login = '' where deleted_at is not null;
alter table cp.users force row level security;
alter table cp.users drop column umami_username;
alter table cp.users drop column stellar_address;
alter table cp.users alter column github_login set not null;
alter table cp.users alter column github_id set not null;
alter table cp.users add constraint users_identity_check check (
  (deleted_at is null and github_id > 0 and github_login <> '')
  or (deleted_at is not null and github_id = 0 and github_login = ''
      and not is_operator and last_login_at is null)
);

-- passkey sign-in (contract 4.1.9). a console account can also be a soropass
-- passkey: a webauthn credential whose p-256 key is the founding signer of a
-- soropass smart account, a stellar contract account (C-address) that the
-- soropass AccountFactory deploys for that credential and key. the account
-- is a third identity on cp.users, beside github and the wallet's
-- G-address, for the reason 00034 gives: an account holds at most one of
-- each kind and every place that names a person reads the one row.
--
--   passkey_address        the C-address, which the control plane derives
--                          from the credential id and the key; it is the
--                          identity that invites and operator entries name
--   passkey_credential_id  the base64url webauthn credential id, which a
--                          sign-in assertion names
--   passkey_public_key     the 65-byte uncompressed sec-1 key that verifies
--                          the assertions
--
-- the three come together. the credential id and the key are never shown to
-- a tenant; members see the address, as they see a wallet's.
--
-- an invite for a passkey account names its C-address in
-- cp.invites.stellar_address, which now takes a contract address as well as
-- an account id: both are stellar addresses, and the strkey prefix tells
-- them apart.
--
-- a passkey challenge is made single use through cp.wallet_challenges_used,
-- as a wallet challenge is (00034): the control plane stores the sha-256 of
-- a value of its own, so the two kinds cannot collide.

-- +goose Up
alter table cp.users add column passkey_address text;
alter table cp.users add column passkey_credential_id text;
alter table cp.users add column passkey_public_key bytea;

alter table cp.users drop constraint users_identity_check;
-- a live row names a github account, a stellar account, a passkey or any
-- of them together; the three passkey columns come together. an emptied
-- row names nothing. the patterns are the strkey shapes; the control plane
-- checks checksums and derives the address before it writes one.
alter table cp.users add constraint users_identity_check check (
  (deleted_at is null
    and (github_id is null) = (github_login is null)
    and (github_id is null or (github_id > 0 and github_login <> ''))
    and (stellar_address is null or stellar_address ~ '^G[A-Z2-7]{55}$')
    and (passkey_address is null) = (passkey_credential_id is null)
    and (passkey_address is null) = (passkey_public_key is null)
    and (passkey_address is null or (passkey_address ~ '^C[A-Z2-7]{55}$'
      and passkey_credential_id ~ '^[A-Za-z0-9_-]+$' and length(passkey_credential_id) <= 1364
      and octet_length(passkey_public_key) = 65 and get_byte(passkey_public_key, 0) = 4))
    and (github_id is not null or stellar_address is not null or passkey_address is not null))
  or (deleted_at is not null
    and github_id is null and github_login is null and stellar_address is null
    and passkey_address is null and passkey_credential_id is null and passkey_public_key is null
    and umami_username is null and not is_operator and last_login_at is null)
);
create unique index users_passkey_address_live_idx on cp.users (passkey_address)
  where deleted_at is null and passkey_address is not null;
create unique index users_passkey_credential_live_idx on cp.users (passkey_credential_id)
  where deleted_at is null and passkey_credential_id is not null;

-- member lists show the address of a passkey member
grant select (passkey_address) on cp.users to cp_tenant;

alter table cp.invites drop constraint invites_stellar_address_check;
alter table cp.invites add constraint invites_stellar_address_check
  check (stellar_address ~ '^[GC][A-Z2-7]{55}$');

-- +goose Down
-- passkey-only accounts and passkey invites cannot be expressed in the old
-- columns, so the down step refuses while any exists
-- +goose StatementBegin
do $$
begin
  if exists (select 1 from cp.users where passkey_address is not null and deleted_at is null) then
    raise exception 'cp.users holds passkey identities; they cannot be restored to the old columns';
  end if;
  if exists (select 1 from cp.invites where stellar_address like 'C%') then
    raise exception 'cp.invites holds passkey invites; they cannot be restored to the old constraint';
  end if;
end
$$;
-- +goose StatementEnd
alter table cp.invites drop constraint invites_stellar_address_check;
alter table cp.invites add constraint invites_stellar_address_check
  check (stellar_address ~ '^G[A-Z2-7]{55}$');
revoke select (passkey_address) on cp.users from cp_tenant;
drop index cp.users_passkey_credential_live_idx;
drop index cp.users_passkey_address_live_idx;
alter table cp.users drop constraint users_identity_check;
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
alter table cp.users drop column passkey_public_key;
alter table cp.users drop column passkey_credential_id;
alter table cp.users drop column passkey_address;

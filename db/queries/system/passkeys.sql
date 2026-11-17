-- operator passkeys (00041) and the account's sign-in passkey (00037), read
-- and written by the stepup package as cp_system.

-- name: ListPasskeys :many
select id, user_id, credential_id, public_key, name, created_at, last_used_at
from cp.operator_passkeys
where user_id = @user_id
order by created_at, id;

-- name: SignInPasskey :one
-- the account's sign-in passkey (4.1.9); both columns are null for an
-- account without one. step_up says whether it also serves the step-up:
-- only when an active roster row names its address, so the roster vouches
-- for that very passkey. a passkey linked to an account the roster names
-- by github or wallet is a sign-in method only; a session alone can link
-- one, so it must not become a second factor.
select passkey_credential_id, passkey_public_key, passkey_address,
       (passkey_address is not null and cp.roster_active(null, null, passkey_address))::boolean as step_up
from cp.users
where id = @id and deleted_at is null;

-- name: AddPasskey :one
-- a credential id already stored for any account fails on the unique index
insert into cp.operator_passkeys (user_id, credential_id, public_key, name)
values (@user_id, @credential_id, @public_key, @name)
returning id, user_id, credential_id, public_key, name, created_at, last_used_at;

-- name: TouchPasskey :execrows
update cp.operator_passkeys
set last_used_at = now()
where user_id = @user_id and credential_id = @credential_id;

-- name: DeletePasskey :one
delete from cp.operator_passkeys
where id = @id and user_id = @user_id
returning id, name;

-- name: DeleteUserPasskeys :execrows
-- every added passkey of an account: a roster reset, a removal, an account
-- deletion. the sign-in passkey goes with the account's identity, not here
delete from cp.operator_passkeys where user_id = @user_id;

-- name: LockUserPasskeys :many
-- the added passkeys of an account, locked, so the last one cannot be
-- deleted by two requests at once
select id from cp.operator_passkeys where user_id = @user_id for update;

-- name: CountPasskeys :one
select count(*)::bigint from cp.operator_passkeys where user_id = @user_id;

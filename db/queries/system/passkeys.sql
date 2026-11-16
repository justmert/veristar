-- operator passkeys (00039), read and written by the stepup package as
-- cp_system.

-- name: ListPasskeys :many
select id, user_id, credential_id, credential, name, created_at, last_used_at
from cp.operator_passkeys
where user_id = @user_id
order by created_at, id;

-- name: CountPasskeys :one
select count(*)::bigint from cp.operator_passkeys where user_id = @user_id;

-- name: AddPasskey :one
-- a credential id already stored for any account fails on the unique index
insert into cp.operator_passkeys (user_id, credential_id, credential, name)
values (@user_id, @credential_id, @credential, @name)
returning id, user_id, credential_id, credential, name, created_at, last_used_at;

-- name: TouchPasskey :execrows
-- stores the credential record after an assertion: the sign count and the
-- backup state the authenticator reported
update cp.operator_passkeys
set credential = @credential, last_used_at = now()
where user_id = @user_id and credential_id = @credential_id;

-- name: DeletePasskey :one
delete from cp.operator_passkeys
where id = @id and user_id = @user_id
returning id, name;

-- name: DeleteUserPasskeys :execrows
-- every passkey of an account: a roster reset, a removal, an account deletion
delete from cp.operator_passkeys where user_id = @user_id;

-- name: LockUserPasskeys :many
-- the passkeys of an account, locked, so the last one cannot be deleted by
-- two requests at once
select id from cp.operator_passkeys where user_id = @user_id for update;

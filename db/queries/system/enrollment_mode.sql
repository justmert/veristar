-- the enrollment mode setting (control-plane-api 5.7), written by operators
-- under cp_system. enrollment reads it with GetSetting.

-- name: LockEnrollmentMode :one
-- the row lock makes two concurrent switches write their audit rows in
-- order, so each row's old value is the value the switch replaced
select value from cp.settings where key = 'enrollment_mode' for update;

-- name: SetEnrollmentMode :exec
update cp.settings set value = @value where key = 'enrollment_mode';

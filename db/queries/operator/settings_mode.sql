-- name: OperatorEnrollmentMode :one
-- the enrollment mode as operators read it (control-plane-api 5.7); reads of
-- cp.settings run as cp_operator (5.1)
select value from cp.settings where key = 'enrollment_mode';

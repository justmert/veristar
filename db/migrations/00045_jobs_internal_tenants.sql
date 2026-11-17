-- the integrity job grades every site of the registry hourly, the
-- platform's own probe project included. an internal project is in no
-- operator view, so its grades only added noise to the job's grade
-- metrics. the job reads is_internal to leave those projects out; 00019
-- already lets it read id and status under the same policy.

-- +goose Up
grant select (is_internal) on cp.tenants to jobs;

-- +goose Down
revoke select (is_internal) on cp.tenants from jobs;

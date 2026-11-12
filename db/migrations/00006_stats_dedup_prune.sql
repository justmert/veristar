-- the jobs task statsdedup-prune removes cp.stats_dedup ids older than 8
-- days. EDGE_STATS keeps messages for at most 7 days, so a pruned id can no
-- longer arrive again and the table stops growing without bound.

-- +goose Up
grant delete on cp.stats_dedup to jobs;
-- the prune deletes by age; without an index every run reads the whole table
create index stats_dedup_applied_at_idx on cp.stats_dedup (applied_at);

-- +goose Down
drop index cp.stats_dedup_applied_at_idx;
revoke delete on cp.stats_dedup from jobs;

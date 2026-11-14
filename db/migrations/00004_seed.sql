-- initial settings and fence rows.
--
-- the application roles may update a fence row but not create one, so the row
-- for every fenced worker (metricswriter and jobs) exists from the
-- start. revision 0 is below any nats kv revision, so the first leader wins.

-- +goose Up
insert into cp.settings (key, value) values
  ('enrollment_mode', '"open"'::jsonb),
  ('removal_keep_aggregates', 'false'::jsonb);

insert into cp.leader_fence (name, revision) values
  ('metricswriter', 0),
  ('jobs', 0);

-- +goose Down
delete from cp.leader_fence where name in ('metricswriter', 'jobs');
delete from cp.settings where key in ('enrollment_mode', 'removal_keep_aggregates');

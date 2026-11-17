-- the api rollup reads when the first traffic class rule set took effect.
-- during the rolling deploy of 00040 the rollup of the release before it can
-- still run on a node that is not updated yet; it groups without the class
-- and stores every class of an hour under 'app'. the first consistency check
-- of a new jobs process compares hourly rows with their minutes per class
-- from that moment on, so such an hour is recomputed. the table holds no
-- tenant data; jobs only reads it.

-- +goose Up
grant select on api.traffic_class_rules to jobs;
alter policy traffic_class_rules_read on api.traffic_class_rules
  to cp_tenant, cp_operator, cp_system, jobs;

-- +goose Down
alter policy traffic_class_rules_read on api.traffic_class_rules
  to cp_tenant, cp_operator, cp_system;
revoke select on api.traffic_class_rules from jobs;

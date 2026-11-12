-- erasure requests (the design, control-plane-api section 10). the table
-- exists since 00002; this adds what the workflow records about each run.
-- it still never holds an ip address, a user agent or a visitor token: those
-- live in control plane memory for the run only.

-- +goose Up
alter table cp.erasure_requests
  -- erased: something was deleted. nothing_found: the request was valid but
  -- matched no data (a visitor_today request after the visit's utc day, an
  -- unknown session id)
  add column outcome text check (outcome in ('erased', 'nothing_found')),
  -- english explanation for the tenant, also set for nothing_found
  add column message text,
  -- visitor_today: the utc day whose visitor secret produced the token
  add column day date,
  -- session: the umami session id the tenant supplied
  add column session_id uuid,
  -- the control plane process running the request renews this; the jobs
  -- task erasure-followup fails a running request whose renewal stopped
  add column heartbeat_at timestamptz,
  -- utc days whose aggregates must be read again; null when none changed
  add column affected_from date,
  add column affected_to date,
  -- set by erasure-followup once agg.web_daily matches umami again
  add column reaggregated_at timestamptz,
  add constraint erasure_requests_kind_fields check (
    (kind = 'visitor_today') = (day is not null)
    and (kind = 'session') = (session_id is not null)),
  add constraint erasure_requests_affected check (
    (affected_from is null) = (affected_to is null) and affected_from <= affected_to),
  add constraint erasure_requests_done check (
    (status in ('done', 'failed')) = (completed_at is not null)
    and (status = 'done') = (outcome is not null)
    and (status = 'failed') = (error is not null));

-- one request at a time per site, so a site reset and a visitor erasure
-- never interleave their umami calls
create unique index erasure_requests_one_active_idx on cp.erasure_requests (site_id)
  where status in ('pending', 'running');
create index erasure_requests_followup_idx on cp.erasure_requests (requested_at)
  where status = 'running' or (status in ('done', 'failed') and reaggregated_at is null and affected_from is not null);

-- +goose Down
drop index cp.erasure_requests_followup_idx;
drop index cp.erasure_requests_one_active_idx;
alter table cp.erasure_requests
  drop constraint erasure_requests_done,
  drop constraint erasure_requests_affected,
  drop constraint erasure_requests_kind_fields,
  drop column reaggregated_at,
  drop column affected_to,
  drop column affected_from,
  drop column heartbeat_at,
  drop column session_id,
  drop column day,
  drop column message,
  drop column outcome;

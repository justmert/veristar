---
title: "Tenant suspension and removal"
description: "Suspend, resume and remove a project, and follow a removal to completion."
---

How an operator suspends, resumes and removes a project, what happens to its
data, and how to follow or repair a removal. Contract:
`docs/reference/control-plane-api.md` sections 5.5, 5.11 and 5.13. Code:
`internal/controlplane/lifecycle`. Every action is an operator route: a
console session of a user whose GitHub id is in `OPERATOR_GITHUB_IDS`
([operators.md](operators.md)).

## Suspending a project

Suspension stops ingestion and keeps everything else: use it while a
question about a project is open (traffic that looks forged, a project that
may not be part of the ecosystem, an abuse report), or as the reversible
first step before removal.

1. In the console operator area open the project and choose **Suspend**, or
   call `POST /api/operator/tenants/{tenant_id}/suspend` with
   `{"reason": "..."}`. The reason is required and the project's members see
   it. Only an `active` project can be suspended; anything else answers
   `409 invalid_transition` with the current status.
2. In one transaction the status becomes `suspended` with an audit row
   (`tenant.suspend`), then the reconciler switches the project's entries in
   the `registry_sites` and `registry_otlp` buckets to `suspended`. From then
   on the gateways refuse the project's web events and the collectors refuse
   its OTLP tokens. Refusals are counted, so the project's ingest view shows
   them.
3. The Umami dashboards stay readable for the members. Data already stored
   stays. Members see the status and reason in the console.
4. Check it took effect:

   ```sql
   select status, status_reason, status_changed_at from cp.tenants where id = '<tenant id>';
   select at, action, detail from cp.audit_log
   where detail ->> 'tenant_id' = '<tenant id>' order by at desc limit 5;
   ```

   (run on the leader as in [overview.md](overview.md)). The gateway's refusal of
   a suspended site is `rejected_inactive_site` in `cp.web_ingest_hourly`.

**Resume** with **Resume** or `POST /api/operator/tenants/{tenant_id}/resume`
(`reason` optional). The registry entries switch back to `active` and
ingestion restarts. Events sent while suspended were refused, not queued, and
do not come back.

## Open question for SCF

**After removal, are the anonymous daily aggregates of a project deleted or
kept?** SCF has not answered yet.

- Default (the answer assumed until SCF decides): **deleted**.
- Setting: `removal_keep_aggregates` in `cp.settings`, changed by an operator
  with `PUT /api/operator/settings/removal-keep-aggregates`
  (`{"keep": true}`). Every change is in the audit log as
  `settings.removal_keep_aggregates`.
- The value is copied into `cp.tenant_removals.keep_aggregates` at the moment
  a project is removed. Changing the setting later never changes a removal
  that already started.
- `keep = true` keeps `cp.web_ingest_hourly` rows and, from this stage, the
  `agg` rows of the project. Umami websites with their raw events, and from
  this stage the `api` minute rows, are always deleted.

When SCF answers, record the answer and the date here and set the setting to
match.

## Removing a project

1. In the console operator area, open the project and choose **Remove**, or
   call `POST /api/operator/tenants/{tenant_id}/remove` with
   `{"reason": "..."}`. The reason is required and the project's members see
   it.
2. In one transaction the control plane sets the project to `removed`, sets
   every site to `removed` (domains stay on the rows), deletes open invites,
   inserts the `cp.tenant_removals` row and writes the `tenant.remove` audit
   row.
3. The reconciler then purges, store by store, in this order. Ingestion stops
   first, data goes after:

   | Step | Store | What is deleted |
   |---|---|---|
   | `registry` | NATS KV `registry_sites` | the key of every site, so the gateway refuses new events |
   | `otlp_tokens` | `cp.otlp_tokens`, `registry_otlp` (this stage) | tokens revoked |
   | `umami` | Umami | both websites of every site with their data, the team, and the Umami users of members who belong to no other project that is not removed |
   | `web_ingest` | `cp.web_ingest_hourly` | the project's rows, unless `keep_aggregates` |
   | `api` | `api` schema (this stage) | minute rows, series and resource state, route caps and rollup marks always; hourly, daily and warning rows unless `keep_aggregates`. `ingest_dedup` ids stay until they expire (8 days), so a message still queued is not applied twice |
   | `agg` | `agg` schema (this stage) | aggregate rows, unless `keep_aggregates` |

   Each finished step is stored in `cp.tenant_removals.steps` and audited as
   `purge.step` (actor `system:purge`). When every step is done,
   `completed_at` is set and `purge.complete` is written.
4. The project row, its member rows and every audit row remain. Former
   members get 404 on every project route; `/api/me` lists the project under
   `removed_tenants` with the reason.

A domain of a removed project goes to the review queue when anyone registers
it again (or a parent or subdomain of it), even in `open` mode.

## Following a removal

```sql
select tenant_id, requested_at, keep_aggregates, completed_at, steps
from cp.tenant_removals where tenant_id = '<id>';

select at, action, detail from cp.audit_log
where detail ->> 'tenant_id' = '<id>' and action like 'purge.%' order by at;
```

The operator api shows the same as `removal` in
`GET /api/operator/tenants/{tenant_id}` and in `GET /api/operator/audit?tenant_id=<id>`.

## A step that keeps failing

A failed step is retried on every reconcile (after each change, and by the
`jobs` task `reconcile` every 10 minutes). Later steps wait for it. The error
is in the control plane or jobs log with the step name and tenant id.

- `umami website ... belongs to team ...`: an Umami website with the site's id
  belongs to another team. Do not delete it blindly: find out why the ids
  clash, then delete or move it by hand and let the next reconcile continue.
- Umami or NATS unreachable: nothing to do once they are back.
- `removed tenant has no cp.tenant_removals row`: the status was set to
  `removed` by hand in the database. Removal must go through the operator api.
  Set the previous status back and remove the project through the api.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| this stage | Operator lifecycle and console end-to-end suites | operator lifecycle 6/6, console e2e 35/35 | plan status, this stage |
| 2026-11-13 | this stage: `go test -race -run 'TestLifecycle/(SuspendResume\|Remove\|OperatorIDRemoved\|PurgeResumes)$' ./internal/controlplane/lifecycle/` (real PostgreSQL, NATS and Umami containers; the operator routes this runbook calls, signed in through the test-only OAuth fixture) | `SuspendResume`, `Remove`, `PurgeResumes` and `OperatorIDRemoved` pass in 19.7 s | this page |

Not yet exercised by a person through the console of a deployed
environment: no staging exists. The SCF answer on whether aggregates are kept
after removal is still open (section above).

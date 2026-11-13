# Erasure requests

What an operator does when a data subject or a tenant asks for data to be erased. The statement
of what can be erased and why is [../compliance/erasure.md](../compliance/erasure.md); the API is
[../reference/control-plane-api.md](../reference/control-plane-api.md) section 10. Erasure is
done by the project's **owner** in the console (Privacy page) or with
`POST /api/tenants/{tenant_id}/erasure`. Operators do not have a route that erases a project's
web data for it, on purpose: the service does not know visitors, and only the project can
identify the site and session concerned.

## Who asks, and what to do

| Request comes from | What it covers | Action |
|---|---|---|
| A visitor of a tenant's site, to the tenant | that visitor's data on that site | the tenant's owner files the request; point them to the Privacy page and to step 2 below |
| A visitor, to SCF or the operators directly | a site they visited | the platform cannot identify them; forward to the tenant (the controller for its site), keep the request record outside the platform without the address or user agent |
| A tenant, for a whole site | every event and session of a site | the operator files `site_reset` |
| A tenant leaving | the whole project | removal ([tenant-removal.md](tenant-removal.md)) |

## Steps (owner, with operator support)

1. **Choose the kind** ([../compliance/erasure.md](../compliance/erasure.md) section 1):
   - `visitor_today`: the visitor gives their IP address and user agent, **the same UTC day** as
     the visit. After midnight UTC the day's secret is gone and nothing can be found; the answer
     says `nothing_found`.
   - `session`: the operator finds the session in the Umami dashboard (by time and page) and gives
     its session id. Works for the whole raw retention.
   - `site_reset`: the operator types the site's domain again to confirm.

2. **File it** on the project's Privacy page (`/projects/<tenant id>/privacy`). The page keeps the
   typed address and user agent in memory only. The answer is `202` and the request appears in
   the history with status `running`.

3. **Wait for the result.** The run first waits until every event of the site accepted before
   the request has left `WEB_EVENTS`, so a queued event is erased too. If that does not happen
   within `ERASURE_QUEUE_WAIT` (15 minutes by default) the run fails **without deleting
   anything** and says why: check the queue ([queue-backlog-and-dlq.md](queue-backlog-and-dlq.md))
   and file it again. One request per site runs at a time; a second answers
   `409 erasure_in_progress`.

4. **Read the outcome** in the history or with `GET /api/tenants/{tenant_id}/erasure/{request_id}`:
   `outcome`, `message`, `sessions_deleted`, `events_deleted`, `dlq_messages_deleted` and the
   affected days. `reaggregated_at` is set once the jobs task `erasure-followup` has re-read the
   operator aggregates for those days, so the global views stop counting the erased visits.

5. **If a request stays `running`**, the control plane process that ran it probably stopped.
   `erasure-followup` marks such a run failed after 2 minutes without a heartbeat. File it again:
   for `visitor_today` the token existed only in that process.

6. **Answer the data subject** through the tenant. Data deleted here stays in database backups
   until they expire (pgBackRest keeps four weekly full backups,
   [point-in-time-restore.md](point-in-time-restore.md)); a restore brings it back and the
   request must then be filed again.

## Operator checks

The records keep no address, user agent or visitor token. To see requests across projects, on
the database leader ([README.md](README.md)):

```sql
select id, tenant_id, site_id, kind, status, outcome, requested_at, completed_at, reaggregated_at
from cp.erasure_requests order by requested_at desc limit 20;
```

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| this stage | Erasure suite and the console privacy page | three kinds pass, live e2e 3/3 | plan status, this stage |
| 2026-11-13 | this stage: `go test -race -run TestErasure ./internal/controlplane/erasure/` (real PostgreSQL, NATS and Umami, the gateway's visitor code, the replayer and the jobs follow-up) | 7 of 7 subtests pass in 27.4 s: `validation and roles`, `visitor today`, `queued event and dlq`, `next day`, `session`, `site reset`, `no residue` | this page |

Not yet exercised by an owner on a deployed environment: no staging exists. The legal wording
is pending an operator input.

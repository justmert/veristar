# Removing a project

Operators remove projects that are not part of the Stellar ecosystem (RFP 3.1). Removal is
final: there is no restore. Suspension ([review-and-enrollment.md](review-and-enrollment.md))
is the reversible alternative while a question is being cleared up.

The step-by-step runbook, with the SQL to follow a removal and what to do when a purge step
keeps failing, is [../runbooks/tenant-removal.md](../runbooks/tenant-removal.md). The
contract is `docs/reference/control-plane-api.md` sections 5.5, 5.13 and 8.7.

## How to remove

On the **Projects** tab open the project and choose **Remove**, or call
`POST /api/operator/tenants/{tenant_id}/remove` with `{"reason": "..."}`. The reason is
required. The project's former members see it once as a notice when they next open the
console.

## What happens

At once, in one transaction: the project and every site become `removed`, open invites are
deleted, the removal is recorded with the current value of the aggregates setting, and
`tenant.remove` is written to the audit log. From that moment every project page answers 404
to its former members.

Then the purge runs store by store, ingestion first and data after:

| Step | Effect |
|---|---|
| `registry` | the gateway refuses new events for every site of the project |
| `otlp_tokens` | every OTLP token is revoked; the collector refuses them |
| `umami` | both web dashboards (primary and quarantine) of every site are deleted with all their raw events and sessions, then the project's dashboard team, then the dashboard accounts of members who belong to no other project |
| `web_ingest` | gateway outcome counts of the project, unless aggregates are kept |
| `api` | per-minute API data always; hourly and daily API data unless aggregates are kept |
| `agg` | daily web aggregates, unless aggregates are kept |

Each finished step is audited as `purge.step`, the end as `purge.complete`. The project page
in the operator area shows the steps and whether data is still being deleted. A step that
fails is retried on the next reconcile, at most 10 minutes later.

What stays: the project record, its member list and every audit row. Former members keep
their console sign-in; they lose access to the project only.

## Keeping or deleting aggregates

The **Settings** tab has **Aggregates of removed projects**. When set to keep, a project
removed from then on keeps its anonymous daily counts (web aggregates, gateway outcome
counts, hourly and daily API data) in the tables the global views and the metrics API read.
Raw web data, the web dashboards and per-minute API data are deleted either way.

- The default is **delete**. Whether SCF wants removed projects' aggregates kept is an open
  question. Record SCF's answer there and
  set the setting to match.
- The value is copied into each removal when it starts, so changing it never changes a removal
  in progress.
- Every change is audited as `settings.removal_keep_aggregates`.

## When the domain comes back

A new site whose domain equals, is a subdomain of or is a parent of a removed project's
domain goes to the review queue, even in `open` enrollment mode, and its project page shows
that the domain was previously removed. Approve or reject it there.

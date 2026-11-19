---
title: "Removing a project"
description: "Remove a project that is not part of the Stellar ecosystem."
---

Operators remove projects that are not part of the Stellar ecosystem. Removal is final: there is
no restore. Suspension ([review-and-enrollment.md](review-and-enrollment.md)) is the reversible
alternative while you clear up a question.

This page is the console task. The runbook [Tenant suspension and removal](../runbooks/tenant-removal.md)
has the database queries to follow a removal and the steps to take when a purge step keeps
failing. The HTTP routes are sections 5.5, 5.13 and 8.7 of the
[control plane API reference](../reference/control-plane-api.md).

## How to remove

On the **All projects** page, open the project, choose **Remove project** in the **Actions**
menu, type the project's slug and press **Remove project**, or call
`POST /api/operator/tenants/{tenant_id}/remove` with `{"reason": "..."}`. The reason is
required. The project's former members see it once as a notice when they next open the
console.

### From the command line

`platformctl tenant remove` performs the same removal in the same transaction, with
`system:platformctl-tenant` as the audit actor. Run it on the core node that holds the Patroni
leader, with the flags of [bootstrap step 10](../deploy/bootstrap.md):

```sh
ssh <core node> sudo docker run --rm --network veristar-core_default \
  --env-file /etc/veristar/env/platformctl.env \
  -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
  -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
  -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
  ghcr.io/justmert/veristar/platformctl:<release> \
  tenant remove -slug <slug> -reason "<reason>"
```

Name the project with `-slug` or with `-tenant <tenant id>`. Run it with `-dry-run` first: it
performs the removal, prints the project's id, slug, name, site count and the status it would
have, and rolls back. The command refuses the internal monitoring project. The jobs `reconcile`
task starts the purge within 10 minutes.

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
| `umami` | both Umami websites (primary and quarantine) of every site are deleted with all their raw events and sessions, then the project's Umami team, then the Umami users of members who belong to no other project |
| `web_ingest` | the network keys always; gateway outcome, evidence and page signal counts of the project unless aggregates are kept |
| `api` | per-minute API data always; hourly and daily API data unless aggregates are kept |
| `agg` | daily web aggregates, integrity grades and coverage counts, unless aggregates are kept |
| `chain` | the ledger read state, week members and open weeks always; daily ledger counts, daily calls per contract function and closed weeks unless aggregates are kept |
| `reports` | every signed usage report of the project, whatever the aggregates setting says |

Each finished step is audited as `purge.step`, the end as `purge.complete`. The project page
in the operator area shows the steps and whether data is still being deleted. A step that
fails is retried on the next reconcile, at most 10 minutes later.

What stays: the project record, its member list and every audit row. Former members keep
their console sign-in; they lose access to the project only.

## Keeping or deleting aggregates

The **Settings** page has **Daily totals of removed projects**. When set to **Keep the daily
totals**, a project
removed from then on keeps its anonymous daily counts (web aggregates and grades, gateway
outcome, evidence and signal counts, hourly and daily API data, daily ledger counts and
closed ledger weeks, daily calls per contract function) in the tables the global views and the metrics API read. Raw web data,
the Umami websites, per-minute API data, the network keys, the ledger read state, week
members and open weeks, and the signed usage reports are deleted either way.

- The default is **Delete the daily totals**. Through the API the setting is
  `PUT /api/operator/settings/removal-keep-aggregates` with `{"keep": true}` or
  `{"keep": false}`.
- The value is copied into each removal when it starts, so changing it never changes a removal
  in progress.
- Every change is audited as `settings.removal_keep_aggregates`.

## When the domain comes back

A new site whose domain equals, is a subdomain of or is a parent of a removed project's
domain goes to the review queue, even in `open` enrollment mode, and its project page shows
that the domain was previously removed. Approve or reject it there.

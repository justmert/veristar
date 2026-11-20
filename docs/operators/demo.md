---
title: "The public demo"
description: "Show projects of sample data at /demo, without sign-in, and take them out again."
icon: "eye"
---

The console has a public demo at `https://app.<BASE_DOMAIN>/demo`. Anyone can open it without
signing in and browse the projects you mark as demo projects: their overview, web, API, on-chain
and "What SCF sees" pages, and the operator views over those projects. Mark only projects that hold
sample data. The demo shows them to everyone.

The HTTP routes are section 21 of the [control plane API reference](../reference/control-plane-api.md).

## What the demo shows

- The pages of each demo project that read data: overview, web analytics, API metrics, on-chain
  activity, sites, integrity, cohort, signed reports and "What SCF sees".
- The operator views, limited to demo projects: the project list and detail, the global web and
  API views and their comparisons, cohorts, and threshold rules with their matches. Totals,
  counts, comparisons and rule matches include no other project.

Every page is read only. A visitor sees a small banner that the demo holds sample data, with a
link to enroll.

## What the demo never shows

- Any project without the demo flag, including its name, slug, id and counts.
- Any person: members, invites, who created, approved or signed anything, and the audit log.
- OTLP tokens, MCP access keys, operator API keys, erasure requests and site verification tokens.
- The operator alerts, audit log, roster and settings.
- CSV exports.

Every write answers `403 demo_read_only`. The database enforces the same limits: demo requests run
in read-only transactions, and the operator views run as the database role `cp_demo`, which reads
demo projects only.

## Turn a project's demo on or off

Run `platformctl tenant demo` on the core node that holds the Patroni leader, with the flags of
[bootstrap step 10](../deploy/bootstrap.md):

```sh
ssh <core node> sudo docker run --rm --network veristar-core_default \
  --env-file /etc/veristar/env/platformctl.env \
  -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
  -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
  -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
  ghcr.io/justmert/veristar/platformctl:<release> \
  tenant demo -slug <slug> -on
```

Use `-off` to take the project out. Name the project with `-slug` or with `-tenant <tenant id>`.
Run it with `-dry-run` first: it makes the change, prints the project's id, slug, name, status
and both flag values, and rolls back. The command writes a `tenant.demo` audit row with
`system:platformctl-tenant` as the actor. It refuses the internal monitoring project and a
removed project.

The change takes effect at the next request. To close the demo completely, run `-off` for every
demo project. The demo then lists no project.

## Check what is in the demo

List the demo projects on the leader:

```sql
select id, slug, name, status from cp.tenants where demo order by created_at;
```

And see the demo as a visitor sees it, from any machine:

```sh
curl -s https://app.<BASE_DOMAIN>/api/demo/me
```

The answer lists the demo projects under `tenants`.

## Limits

Each visitor may send 60 requests at once and then 5 a second; the demo answers
`429 rate_limited` beyond that. The web analytics pages share one limit of 120 Umami reads a
minute across all visitors of the demo.

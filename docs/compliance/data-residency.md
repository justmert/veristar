---
title: "Data residency"
description: "Where each kind of data is processed and stored in the production deployment."
---

This page declares where the production deployment processes and stores each
kind of data. It is not legal advice. Last updated 2026-11-14.

## Declaration

This declaration describes the production deployment defined in
`infra/tofu/envs/production` and `infra/ansible`, running since 2026-11-13
([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md)).
The locations below are the ones the running deployment uses.

All analytics data is hosted in Microsoft Azure, region Denmark East
(`denmarkeast`, EU), on three converged nodes, one in each of the region's
availability zones 1, 2 and 3. Every node runs the ingestion edge and the core,
so every service has one member in each zone.

| What | Where | Provider |
|---|---|---|
| Ingestion edge: Caddy, gateway, OTLP collector, NATS (queues and memory buckets) | Denmark (Azure Denmark East, availability zones 1, 2 and 3, one node each) | Microsoft Azure |
| Core: PostgreSQL under Patroni (both databases, three members: leader, synchronous standby, replica), Umami, control plane, replayer, metrics writer, jobs, console | Denmark (Azure Denmark East, availability zones 1, 2 and 3, on the same three nodes) | Microsoft Azure |
| Backups of PostgreSQL, encrypted by pgBackRest before upload | Denmark (Azure Denmark East, zone-redundant Blob Storage, account `veristarprodbackups`) | Microsoft Azure |
| OpenTofu state of the infrastructure, encrypted with the state passphrase. Holds resource settings and addresses; never analytics data | Sweden (Azure Sweden Central, storage account `veristarprodstate`) | Microsoft Azure |
| Health-checked DNS failover | Azure Traffic Manager, a global DNS service. Sees DNS queries for the service's hostnames and checks the nodes' health endpoint; never analytics data | Microsoft Azure |
| Authoritative DNS and domain registration | Cloudflare's DNS network, DNS only (no proxy: traffic goes straight to Azure). Sees DNS queries for the service's hostnames; never analytics data | Cloudflare |
| External prober and public status page | a separate Azure VM in East US (United States), outside the production deployment and its region ([../deploy/probe.md](../deploy/probe.md)). Holds no analytics data: it probes the public endpoints with its own synthetic traffic for the internal probe tenant and keeps the check results and the daily SLO result files | Microsoft Azure |
| Console sign-in | GitHub OAuth | GitHub, Inc. (United States). Receives only the sign-in of console users; never visitor or API client data |

## What leaves the EU

- **Analytics data: nothing.** Web events, sessions, aggregates and API
  metrics are processed and stored only in Azure Denmark East, in the rows
  above.
- **The external prober.** The prober in East US sends synthetic events for
  the internal probe tenant and reads that tenant's own aggregates back. No
  visitor's or tenant's data reaches it. Its status page at
  `status.veristar.app` shows check results only; Cloudflare serves its DNS
  record without a proxy.
- **Console sign-in.** Console users authenticate with GitHub. GitHub sees that
  a user signs in to the service. The control plane stores the numeric GitHub
  id and login only. This concerns tenant staff, not end users.
- **Operator access.** Operators read aggregates through the console and the
  machine API. Aggregates are counts per site and day. An operator outside
  the EU reading them transfers personal data only if the aggregates are
  personal data (`privacy-strategy.md` section 1). Production access to raw
  data (shell, database) is limited to the people whose age keys are
  recipients of the deployment's secrets (`infra/secrets/recipients/`).
- **EU to US transfers.** None for analytics data. For reference, the EU-US
  Data Privacy Framework was upheld by the General Court in T-553/23 (3
  September 2025) and the appeal C-703/25 P is pending (S9).
- **Access from Turkey.** If a person with production access works from
  Turkey, that access to EU-hosted personal data is a transfer to a country
  without an EU adequacy decision (S7, the Commission's list does not include
  Turkey), and standard contractual clauses are needed on the EU side. On the
  Turkish side, Law 6698 Art. 9 as amended by Law 7499 and the Regulation of
  10 July 2024 require a standard contract to be notified to the KVKK
  Authority within five business days of signature (S7).

## Tracker script

Tenants load `sdk.js` from `in.<BASE_DOMAIN>` (the EU edge) or self-host the
same file. Events always go from the browser to `in.<BASE_DOMAIN>`. No CDN outside the edge serves the tracker.

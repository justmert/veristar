# Data residency

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-12. This is the explicit residency declaration the RFP asks
> for.

## Declaration

This declaration describes the production deployment defined in
`.scratch/plan.md` (hosting choices in section 5) and built in phases 15 and
16. **As of this draft no production deployment exists**, so nothing below is
yet observed in a running system. this stage must make the deployment match this
text, or this text must change before it is published.

| What | Where | Provider |
|---|---|---|
| Ingestion edge: Caddy, gateway, OTLP collector, NATS (queues and memory buckets) | Germany (Falkenstein `fsn1`, Nuremberg `nbg1`) and Finland (Helsinki `hel1`) | Hetzner Online GmbH (Germany) |
| Core: PostgreSQL (both databases), Umami, control plane, replayer, metrics writer, jobs, console | Germany (`fsn1`, `nbg1`) | Hetzner Online GmbH |
| Backups of PostgreSQL | Germany | Hetzner Object Storage (this stage) |
| Authoritative DNS | EU provider | BunnyWay d.o.o. (bunny.net, Slovenia). Sees DNS queries for the service's hostnames only, never analytics data |
| Status page and external prober | France (`fr-par`) | Scaleway SAS. Holds no analytics data: it probes public endpoints with its own synthetic traffic |
| Console sign-in | GitHub OAuth | GitHub, Inc. (United States). Receives only the sign-in of console users; never visitor or API client data |

## What leaves the EU

- **Analytics data: nothing.** Web events, sessions, aggregates and API
  metrics are processed and stored only in the locations above.
- **Console sign-in.** Console users authenticate with GitHub. GitHub sees that
  a user signs in to the service. The control plane stores the numeric GitHub
  id and login only. This concerns tenant staff, not end users.
- **Operator access.** Operators read aggregates through the console and the
  machine API. Aggregates are counts per site and day. Whether an operator
  outside the EU reading them is a transfer of personal data depends on
  whether they are anonymous (open question in `privacy-strategy.md` section
  1). Production access to raw data (shell, database) is limited to the
  people listed in the deployment's secrets recipients (plan an operator input; from
  which countries they work is recorded in `records-of-processing.md` once
  known.
- **EU to US transfers.** None for analytics data. For reference, the EU-US
  Data Privacy Framework was upheld by the General Court in T-553/23 (3
  September 2025) and the appeal C-703/25 P is pending (S9).
- **Access from Turkey.** If a person with production access works from
  Turkey, that access to EU-hosted personal data is a transfer to a country
  without an EU adequacy decision (S7, the Commission's list does not include
  Turkey), and standard contractual clauses are needed on the EU side. On the
  Turkish side, Law 6698 Art. 9 as amended by Law 7499 and the Regulation of
  10 July 2024 require a standard contract to be notified to the KVKK
  Authority within five business days of signature (S7). Which of these
  applies to the actual operators is for counsel.

## Tracker script

Tenants load `sdk.js` from `in.<BASE_DOMAIN>` (the EU edge) or self-host the
same file. Events always go from the browser to `in.<BASE_DOMAIN>`. No CDN
outside the edge serves the tracker in the planned deployment.

---
title: "Records of processing"
description: "The records of processing activities for the service."
---

Two records of processing: the operator as processor for tenants (GDPR Art.
30(2)) and the operator as controller of console accounts (Art. 30(1)). The
records name the operator's role. This page is not legal advice. Last updated
2026-11-14.

## A. Processing on behalf of tenants (operator as processor)

| Item | Entry |
|---|---|
| Processor | the service operator (SCF until handover, then the community operator) |
| Controllers | each enrolled tenant, as listed in `cp.tenants` |
| Categories of processing | collection of web page views and custom events through the tracker; country lookup; daily visitor token computation; queueing; storage and reporting in Umami; daily aggregates for the operator view; collection of HTTP server metrics and spans from tenant applications, reduction to request counts and latency histograms, storage and reporting; erasure on request |
| Categories of data subjects | visitors of tenant websites; clients of tenant APIs (only as anonymous request counts and latencies) |
| Categories of personal data | for web visitors, held in memory only: IP address and user agent. Stored: daily visitor token as part of Umami's session id, browser, operating system, device type, screen size, language, country, page path with allowlisted query, referrer scheme and host, page title, event names and event data. For API clients: none at the level of a person; only tenant, service, normalized route, method, status and latency buckets |
| Recipients | the tenant's members (its own dashboards, and the AI clients they connect through the MCP server, `privacy-strategy.md` section 10); SCF operators (aggregates only, see `privacy-strategy.md` section 1); sub-processors in `subprocessors.md` |
| Transfers to third countries | none for analytics data (`data-residency.md`) |
| Retention | `privacy-strategy.md` section 4 |
| Security measures | `dpia.md` section 5 |

## B. Console accounts (operator as controller)

| Item | Entry |
|---|---|
| Controller | the service operator |
| Purpose | sign-in to the console, membership of projects, audit of changes |
| Data subjects | tenant staff and operators who sign in |
| Personal data | numeric GitHub user id, GitHub login, project memberships and roles, session token hashes, MCP credential hashes with their names and last use, audit log entries naming the user id (MCP tool calls included, without their answers) |
| Recipients | GitHub, Inc. for the sign-in itself (`subprocessors.md`) |
| Transfers | the sign-in passes through GitHub in the United States |
| Retention | sessions: 7 days absolute, 24 hours idle; accounts: until the person deletes the account or the service stops; the audit log: while the service runs |
| Erasure | each person deletes their own account on the console's Account page; GitHub id, login, memberships, sessions, MCP credentials and invites are deleted, and audit rows keep a pseudonymous account id (`erasure.md` section 7) |

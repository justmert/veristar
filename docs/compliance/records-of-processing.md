---
title: "Records of processing"
description: "The records of processing activities for the service."
---

Two records of processing: the hosting operator as processor for tenants (GDPR
Art. 30(2)) and the hosting operator as controller of console accounts (Art.
30(1)). The hosting operator is the party that runs the service and hosts its
data (`terms.md` section 1). SCF does not run the service; it receives project
statistics on each tenant's instruction (`terms.md` section 1.1). This page is
not legal advice. Last updated 2026-11-16.

## A. Processing on behalf of tenants (hosting operator as processor)

| Item | Entry |
|---|---|
| Processor | the hosting operator, named in the repository |
| Controllers | each enrolled tenant, as listed in `cp.tenants` |
| Categories of processing | collection of web page views and custom events through the tracker; country lookup; network classification of each event for fraud detection (access, organisation, hosting, anonymiser or unknown, from the address in memory), reduced to per-site daily counts and an integrity grade; daily visitor token computation; queueing; storage and reporting in Umami; daily aggregates for the operator view and for the project statistics SCF receives; collection of HTTP server metrics and spans from tenant applications, reduction to request counts and latency histograms, storage and reporting; erasure on request |
| Categories of data subjects | visitors of tenant websites; clients of tenant APIs (only as anonymous request counts and latencies) |
| Categories of personal data | for web visitors, held in memory only: IP address and user agent, and the autonomous system of the address. Stored: daily visitor token as part of Umami's session id, browser, operating system, device type, screen size, language, country, page path with allowlisted query, referrer scheme and host, page title, event names and event data; per site and day, events per network class and the distinct networks as keys under a daily secret that is destroyed at midnight, deleted after four days. For API clients: none at the level of a person; only tenant, service, normalized route, method, status and latency buckets |
| Recipients | the tenant's members (its own dashboards, and the AI clients they connect through the MCP server, `privacy-strategy.md` section 10); SCF, which receives the per site and day web aggregates and the hourly and daily API aggregates on the tenant's instruction (`terms.md` section 1.1, `dpa-template.md` clause 3); sub-processors in `subprocessors.md` |
| Transfers to third countries | none for analytics data (`data-residency.md`) |
| Retention | `privacy-strategy.md` section 4 |
| Security measures | `dpia.md` section 5 |

## B. Console accounts (hosting operator as controller)

| Item | Entry |
|---|---|
| Controller | the hosting operator |
| Purpose | sign-in to the console, membership of projects, audit of changes |
| Data subjects | tenant staff and operators who sign in |
| Personal data | for a GitHub sign-in: numeric GitHub user id and GitHub login. For a wallet sign-in: the Stellar account address (the public key) whose key signed the sign-in challenge; it is personal data once it is linked to the person's console account. For both: project memberships and roles, session token hashes, MCP credential hashes with their names and last use, audit log entries naming the user id (MCP tool calls included, without their answers) |
| Recipients | GitHub, Inc. for a GitHub sign-in (`subprocessors.md`). A wallet sign-in has no recipient: the wallet extension runs in the person's own browser, and the challenge it signs never reaches the Stellar network |
| Transfers | a GitHub sign-in passes through GitHub in the United States; a wallet sign-in passes through no third party |
| Retention | sessions: 7 days absolute, 24 hours idle; accounts: until the person deletes the account or the service stops; the audit log: while the service runs |
| Erasure | each person deletes their own account on the console's Account page; GitHub id, login, Stellar address, memberships, sessions, MCP credentials and invites are deleted, and audit rows keep a pseudonymous account id (`erasure.md` section 7). Unlinking a sign-in method removes that identity from the account at once |

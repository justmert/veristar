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
| Categories of processing | collection of web page views and custom events through the tracker; country lookup; network classification of each event for fraud detection (access, organisation, hosting, anonymiser or unknown, from the address in memory), reduced to per-site daily counts and an integrity grade; per-site daily counts of the page signals tracker 1.3.0 reports (framed page views, pages left without being visible) for the same grade; per-site daily counts of the requests for the tracker file and of those carrying Global Privacy Control, attributed to the site by the page origin in the `Referer` header, to estimate the visits that are not measured; daily visitor token computation; queueing; storage and reporting in Umami; daily aggregates for the operator view and for the project statistics SCF receives; signed usage reports the tenant creates from those aggregates, and their public links; collection of HTTP server metrics and spans from tenant applications, reduction to request counts and latency histograms, storage and reporting; erasure on request |
| Categories of data subjects | visitors of tenant websites; clients of tenant APIs (only as anonymous request counts and latencies) |
| Categories of personal data | for web visitors, held in memory only: IP address and user agent, and the autonomous system of the address. Stored: daily visitor token as part of Umami's session id, browser, operating system, device type, screen size and language (sent by tracker releases before 1.3.0 only), country, page path with allowlisted query, referrer scheme and host, page title (from tracker 1.3.0 only when the site asks for it), event names and event data; per site and day, events per network class and the distinct networks as keys under a daily secret that is destroyed at midnight, deleted after four days. The `Referer` and `Sec-GPC` headers of a request for the tracker file are read in memory for the request and never stored; only per-site daily counts remain. For API clients: none at the level of a person; only tenant, service, normalized route, method, status and latency buckets |
| Recipients | the tenant's members (its own dashboards, and the AI clients they connect through the MCP server, `privacy-strategy.md` section 10); anyone the tenant gives the link of a signed usage report, which holds aggregates only (`privacy-strategy.md` section 12); SCF, which receives the per site and day web aggregates and the hourly and daily API aggregates on the tenant's instruction (`terms.md` section 1.1, `dpa-template.md` clause 3); sub-processors in `subprocessors.md` |
| Transfers to third countries | none for analytics data (`data-residency.md`) |
| Retention | `privacy-strategy.md` section 4 |
| Security measures | `dpia.md` section 5 |

## B. Console accounts (hosting operator as controller)

| Item | Entry |
|---|---|
| Controller | the hosting operator |
| Purpose | sign-in to the console, membership of projects, audit of changes |
| Data subjects | tenant staff and operators who sign in |
| Personal data | for a GitHub sign-in: numeric GitHub user id and GitHub login. For a wallet sign-in: the Stellar account address (the public key) whose key signed the sign-in challenge; it is personal data once it is linked to the person's console account. For a passkey sign-in: the passkey's credential id, its public key and the address of the soropass smart account that key controls (a Stellar contract address derived from the two); no biometric data, which never leaves the person's device. For all: project memberships and roles, the cohort a member proposed for a project and the user id of the member who created or revoked a usage report or set a cohort, session token hashes, MCP credential hashes with their names and last use, audit log entries naming the user id (MCP tool calls included, without their answers) |
| Recipients | GitHub, Inc. for a GitHub sign-in (`subprocessors.md`). A wallet sign-in has no recipient: the wallet extension runs in the person's own browser, and the challenge it signs never reaches the Stellar network. A passkey sign-in has no recipient: the passkey answers in the person's own browser and device, and the console contacts no Stellar network service for it |
| Transfers | a GitHub sign-in passes through GitHub in the United States; a wallet sign-in and a passkey sign-in pass through no third party |
| Retention | sessions: 7 days absolute, 24 hours idle; accounts: until the person deletes the account or the service stops; the audit log: while the service runs |
| Erasure | each person deletes their own account on the console's Account page; GitHub id, login, Stellar address, passkey (credential id, public key and account address), memberships, sessions, MCP credentials and invites are deleted, and audit rows keep a pseudonymous account id (`erasure.md` section 7). Unlinking a sign-in method removes that identity from the account at once |

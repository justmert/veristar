# Records of processing

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-12. Two records: the operator as processor for tenants
> (GDPR Art. 30(2)) and the operator as controller of console accounts (Art.
> 30(1)). The operator's name and contact are filled in when the operating
> entity is decided (open question for SCF, the design); until then this
> record names the role, not the entity.

## A. Processing on behalf of tenants (operator as processor)

| Item | Entry |
|---|---|
| Processor | the service operator (SCF until handover, then the community operator) |
| Controllers | each enrolled tenant, as listed in `cp.tenants` |
| Categories of processing | collection of web page views and custom events through the tracker; country lookup; daily visitor token computation; queueing; storage and reporting in Umami; daily aggregates for the operator view; collection of HTTP server metrics and spans from tenant applications, reduction to request counts and latency histograms, storage and reporting; erasure on request |
| Categories of data subjects | visitors of tenant websites; clients of tenant APIs (only as anonymous request counts and latencies) |
| Categories of personal data | for web visitors, held in memory only: IP address and user agent. Stored: daily visitor token as part of Umami's session id, browser, operating system, device type, screen size, language, country, page path with allowlisted query, referrer, page title, event names and event data. For API clients: none at the level of a person; only tenant, service, normalized route, method, status and latency buckets |
| Recipients | the tenant's members (its own dashboards); SCF operators (aggregates only, see `privacy-strategy.md` section 1); sub-processors in `subprocessors.md` |
| Transfers to third countries | none for analytics data (`data-residency.md`) |
| Retention | `privacy-strategy.md` section 4 |
| Security measures | `dpia.md` section 5 |

## B. Console accounts (operator as controller)

| Item | Entry |
|---|---|
| Controller | the service operator |
| Purpose | sign-in to the console, membership of projects, audit of changes |
| Data subjects | tenant staff and operators who sign in |
| Personal data | numeric GitHub user id, GitHub login, project memberships and roles, session token hashes, audit log entries naming the user id |
| Legal basis | to be decided by counsel (performance of the service terms is the working assumption) |
| Recipients | GitHub, Inc. for the sign-in itself (`subprocessors.md`) |
| Transfers | the sign-in passes through GitHub in the United States |
| Retention | sessions: 7 days absolute, 24 hours idle; accounts and the audit log: while the service runs (deletion of a console account on request is not implemented as a self-service route yet) |

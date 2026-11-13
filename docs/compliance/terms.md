---
version: 2026-11-13-draft.3
status: draft
effective: 2026-11-13
---

# Terms of use

> **Status: draft, pending legal review an operator input.** Not legal advice.
> Projects accept this version when they enroll. When a new version is
> published, every project accepts it at its next sign-in, and changes to a
> project are blocked until it does. The final version drops `draft` from the
> version and the status.

## 1. Parties and roles

- **Operator**: the party that runs this service. Today that is the Stellar
  Community Fund (SCF); after handover it is the community operator named in the
  repository.
- **Tenant**: a project enrolled in the service, represented by the GitHub
  accounts that are members of it.
- For the data of a tenant's website visitors and API clients, the tenant is the
  **controller** and the operator is the **processor**. The operator processes
  that data only to provide the service, following the tenant's documented
  instructions, which are the tenant's configuration of the service (sites,
  allowed origins, tokens, members).
- The data processing terms in `docs/compliance/dpa-template.md` (GDPR Article
  28: purpose, duration, categories of data, sub-processors, security, help with
  data subject requests, deletion at the end of the service) are part of these
  terms. Sub-processors are listed in `docs/compliance/subprocessors.md`.

## 2. No fees

The service is free. No fees are charged to tenants, to SCF or to anyone else.
There is no billing, no paid tier, no advertising and no sale or resale of data.

## 3. What is collected and what is not

- **Websites**: page views and custom events sent by the tracker script. The
  tracker sets no cookies and writes no browser storage. It reads one storage
  key, the visitor's opt-out flag, and honours Global Privacy Control. On a
  site that turns on Wallets Kit capture with the script tag, it also reads
  the names of the site's local storage keys and the kit's selected wallet id
  (`privacy-strategy.md` section 6).
- **IP addresses are never stored.** The ingestion gateway reads a visitor's
  address only in memory, shortens it, looks up the country in the DB-IP
  database, and discards it.
- Visitors are counted with a **daily visitor token** derived from a secret that
  changes every UTC day and is never written to disk. After the day ends, nobody
  can recompute a token, so stored visits cannot be linked to a person.
- **APIs**: request counts and latency histograms only, as aggregates. Traces,
  spans and logs that applications send are discarded and never kept.
- Console accounts: the numeric GitHub user id and GitHub login. No email
  address and no password.

## 4. Acceptable use

Tenants may use the service only:

- for websites and APIs of projects in the Stellar ecosystem;
- for properties the tenant controls;
- without putting **personal data in URLs, page titles, referrers or event
  data**. The service removes Stellar keys, email addresses and secret seeds it
  recognises, but tenants must not rely on that;
- without inflating, forging or replaying traffic;
- without trying to reach other tenants' data;
- without working against the service's limits or security.

## 5. Operator rights

The operator may review projects, and may suspend or remove a project that is not
part of the Stellar ecosystem or that breaks these terms, stating a reason. The
operator may switch enrollment from open to approval-required.

## 6. Retention and deletion

- Raw web events and sessions are deleted after `WEB_RAW_RETENTION_MONTHS`
  months, 25 by default, so dashboards cover 24 full months.
- API metric aggregates are kept for at least 24 months.
- A project owner can erase one visitor's visit of the current UTC day, one
  session at any time, or all web data of a site (`docs/compliance/erasure.md`).
  A visit on an earlier day cannot be linked to a person, by design.
- When a project is removed, its raw web data and per-minute API data are
  deleted; aggregates are deleted unless the operator keeps them under the
  published `removal_keep_aggregates` setting (`docs/runbooks/tenant-removal.md`).

## 7. Availability

The service is designed for 99.99% ingestion availability and 99.90% dashboard
availability. These are targets, not guarantees. A public status page and a
public issue tracker are provided.

## 8. Changes to these terms

A new version must be accepted by each project at its next sign-in. Until it is
accepted, the project can be read but not changed.

## 9. Licences and attribution

- Country data: "IP Geolocation by DB-IP" (https://db-ip.com), licensed under
  Creative Commons Attribution 4.0.
- The platform is open source under the Apache License 2.0. The tracker script is
  under the MIT licence and keeps Umami's copyright notice.

## 10. Contact

Privacy and security questions: the public issue tracker of the repository for
general questions, and the repository's private vulnerability reporting for
security issues. A dedicated privacy contact is named once the operating entity
is decided.

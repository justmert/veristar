---
title: "Terms of use"
description: "The terms of use that every project accepts when it enrolls."
version: 2026-11-17-draft.5
status: draft
effective: 2026-11-17
---


# Terms of use

> **Status: draft.** Not legal advice.
> Projects accept this version when they enroll. When a new version is
> published, every project accepts it at its next sign-in, and changes to a
> project are blocked until it does. The final version drops `draft` from the
> version and the status.

## 1. Parties and roles

- **Hosting operator**: the party that runs this service and hosts its data,
  named in the repository. It runs the service for every tenant alike.
- **SCF**: the Stellar Community Fund. SCF does not run the service and does
  not process the data of a tenant's website visitors or API clients. It
  receives the project statistics of section 1.1.
- **Tenant**: a project enrolled in the service, represented by the console
  accounts (GitHub accounts, Stellar wallets or passkeys) that are members of it.
- For the data of a tenant's website visitors and API clients, the tenant is the
  **controller** and the hosting operator is the **processor** for that tenant.
  The hosting operator processes that data only to provide the service,
  following the tenant's documented instructions: the tenant's configuration of
  the service (sites, allowed origins, tokens, members) and the instruction of
  section 1.1.
- The data processing terms in `docs/compliance/dpa-template.md` (GDPR Article
  28: purpose, duration, categories of data, sub-processors, security, help with
  data subject requests, deletion at the end of the service) are part of these
  terms. Sub-processors are listed in `docs/compliance/subprocessors.md`.

### 1.1 Project statistics for SCF

By enrolling a project and accepting these terms, the tenant instructs the
hosting operator to make aggregate statistics about the project available to
SCF, so that SCF can follow the projects it funds and the ecosystem as a whole.

- What SCF receives:
  - per site and UTC day, counts of page views, visitors, visits and
    bounces, total time, and the ranked referrer hosts, countries, devices,
    browsers and operating systems;
  - per site and UTC day, the integrity grade, its reasons and the counts it
    is computed from: accepted events per network class, distinct networks,
    quarantined and proof-failed requests, visitors, page views, the
    baseline, and the page signal counts of tracker 1.3.0 (page views that
    report signals, framed page views, pages left without being visible);
  - per site and UTC day, the requests for the tracker file and the part of
    them that carried Global Privacy Control, which estimate the share of
    visits that is not measured;
  - per API service, hourly and daily request counts, status classes and
    latency histograms, with application requests apart from preflight,
    health check and uptime probe requests;
  - per declared Stellar account or contract and UTC day, counts of
    operations, transactions and distinct counterparties, and per ISO week
    the project's count of weekly active accounts.
- What SCF does not receive: raw events, sessions, visitor tokens, page-level
  event data, network keys, the keyed hashes of active accounts, the project's
  web dashboard, or the console accounts of the tenant's members. No row SCF
  receives describes one visitor, one API client or one active account.
- The instruction lasts as long as the project is enrolled. It ends when the
  project is removed; the aggregates then follow section 6.

## 2. No fees

The service is free. No fees are charged to tenants, to SCF or to anyone else.
There is no billing, no paid tier, no advertising and no sale or resale of data.

## 3. What is collected and what is not

- **Websites**: page views and custom events sent by the tracker script. The
  tracker sets no cookies and writes no browser storage. It reads one storage
  key, the visitor's opt-out flag, and honours Global Privacy Control. On a
  site that turns on Wallets Kit capture with the script tag, it also reads
  the names of the site's local storage keys and the kit's selected wallet id
  (`privacy-strategy.md` section 6). From tracker 1.3.0 the tracker no
  longer reads the screen size or the browser language, and it sends the page
  title only when the site turns that on with the script tag.
- **Tracker file requests**: when a browser loads the tracker file, the
  gateway reads the request's `Referer` and `Sec-GPC` headers in memory to
  attribute the request to one site, and keeps only two per-site, per-day
  counts: requests, and requests that carry Global Privacy Control. They
  estimate the share of visits the tracker does not measure.
- **IP addresses are never stored.** The ingestion gateway reads a visitor's
  address only in memory, for the request, and discards it. With the whole
  address it computes the daily visitor token below and compares the address
  with the list of Tor exit addresses. It shortens the address to its /24
  (IPv4) or /48 (IPv6) network to look up the country and the autonomous
  system in the local DB-IP databases, and keys its rate limits and its
  same-network checks on an HMAC of that network under a secret that changes
  every UTC day. Only the country, a network class (access, organisation,
  hosting, anonymiser or unknown) counted per site and day, and once per site
  and day an HMAC of the autonomous system number under the same daily secret
  leave the request.
- Visitors are counted with a **daily visitor token** derived from a secret that
  changes every UTC day and is never written to disk. After the day ends, nobody
  can recompute a token, so stored visits cannot be linked to a person.
- **APIs**: request counts and latency histograms only, as aggregates.
  Preflight, health check and uptime probe requests are counted separately
  from application requests; the collector reads the request's user agent in
  memory for that and never stores it. Traces, spans and logs that
  applications send are discarded and never kept.
- **On-chain activity**: for the Stellar accounts and contracts a project
  declares and proves, three counts per id and UTC day (operations,
  transactions, distinct counterparties) and one count of weekly active
  accounts per ISO week. While a week is open, the accounts behind its count
  are held only as keyed hashes under a random key for that week; the hashes
  and the key are deleted when the week closes. No counterparty address is
  stored.
- Console accounts: the numeric GitHub user id and GitHub login of a GitHub
  sign-in, the Stellar account address (public key) of a wallet sign-in, and
  the credential id, public key and smart account address of a passkey
  sign-in. No email address, no password, no private key and no biometric
  data, which never leaves the person's device.
- Operators: the roster row of each operator identity (GitHub id and login,
  or Stellar address), its role, status, status reason and enrollment window;
  the passkeys an operator adds for the operator area (credential id, public
  key, name, creation and last use); the time of the last step-up, in the
  session. They are deleted when an operator admin removes the person from
  the roster or the person deletes the account
  (`docs/compliance/erasure.md` section 7).

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

## 5. Hosting operator rights

The hosting operator may review projects, and may suspend or remove a project
that is not part of the Stellar ecosystem or that breaks these terms, stating a
reason. The hosting operator may switch enrollment from open to
approval-required.

## 6. Retention and deletion

- Raw web events and sessions are deleted after `WEB_RAW_RETENTION_MONTHS`
  months, 25 by default, so dashboards cover 24 full months.
- API metric aggregates are kept for at least 24 months.
- A project owner can erase one visitor's visit of the current UTC day, one
  session at any time, or all web data of a site (`docs/compliance/erasure.md`).
  A visit on an earlier day cannot be linked to a person, by design.
- When a project is removed, its raw web data and per-minute API data are
  deleted; aggregates are deleted unless the hosting operator keeps them under the
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

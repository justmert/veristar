---
title: "Data processing terms (template)"
description: "A template for the data processing terms between the hosting operator and a tenant."
icon: "file-pen"
---

The data processing terms between the hosting operator and each tenant. They
follow GDPR Art. 28(3) clause by clause (S10) and are part of the terms of use
(`terms.md`, section 1) that every project accepts at enrollment. In these
terms "the operator" is the hosting operator of `terms.md` section 1: the party
that runs the service and hosts its data, Mert Köklü (kklumert@gmail.com). SCF is not a party to them; it
receives the project statistics that clause 3 describes. This page is not legal
advice. Last updated 2026-11-18.

## 1. Subject matter, duration, nature and purpose

The operator processes personal data on behalf of the tenant to provide web
analytics and API request metrics for the tenant's registered sites and
services, for as long as the tenant is enrolled, and afterwards only for the
deletion described in clause 9. The processing is described in
`records-of-processing.md` part A.

## 2. Types of data and categories of data subjects

As listed in `records-of-processing.md` part A: website visitors and API
clients of the tenant; the data listed there.

## 3. Documented instructions (Art. 28(3)(a))

The tenant's instructions are its configuration of the service: its sites and
allowed origins, its OTLP tokens, its members, the tracker options it uses and
the erasure requests it makes. They also include the instruction the tenant
gives at enrollment by accepting `terms.md` section 1.1: to make aggregate
statistics about its project available to SCF, limited to the aggregates
listed there (per site and day web aggregates, the integrity grade and the
counts it is computed from, the tracker file and Global Privacy Control
counts, hourly and daily API aggregates, daily on-chain counts, daily calls
per contract function and weekly active accounts), for as long as the project is enrolled. The operator processes personal data only on
these instructions, including with regard to transfers to third countries,
unless EU or member state law requires otherwise, in which case it informs the
tenant first unless that law forbids it. The operator informs the tenant
immediately if, in its opinion, an instruction infringes data protection law.

## 4. Confidentiality (Art. 28(3)(b))

Everyone the operator authorises to process the data is bound to
confidentiality. Production access is limited to the people who hold the
deployment's secrets, whose age keys are the recipients listed in
`infra/secrets/recipients/`.

## 5. Security (Art. 28(3)(c), Art. 32)

The operator takes the measures in `dpia.md` section 5.

## 6. Sub-processors (Art. 28(2) and (4), Art. 28(3)(d))

The tenant gives general authorisation for the sub-processors in
`subprocessors.md`. The operator announces an intended addition or replacement
in the repository's changelog
([CHANGELOG.md](https://github.com/justmert/veristar/blob/main/CHANGELOG.md)) and in a new version of the terms of use,
which every project accepts at its next sign-in (`terms.md` section 8), before it takes effect, so
the tenant can object; if the objection cannot be resolved, the tenant may
leave the service and have its data deleted under clause 9. The operator imposes on each sub-processor the same
data protection obligations as in these terms and remains liable for it.

## 7. Assistance with data subject rights (Art. 28(3)(e))

The operator provides the erasure workflow (`erasure.md`) and the dashboards
through which the tenant can find a session. Requests that reach the operator
directly are forwarded to the tenant. Limits that follow from the design (a
visitor cannot be identified after the UTC day of the visit) are stated in
`erasure.md` section 2.

## 8. Assistance with Arts. 32 to 36 (Art. 28(3)(f))

The operator provides `dpia.md`, `privacy-strategy.md` and
`data-residency.md`, and notifies the tenant without undue delay after
becoming aware of a personal data breach affecting its data, with the
information Art. 33(3) requires as far as it is available.

## 9. Deletion or return at the end (Art. 28(3)(g))

When a tenant is removed (a tenant that wants to leave asks the operator to
remove it), the operator deletes the tenant's raw web
data (Umami websites with their events and sessions), revokes its OTLP tokens,
and deletes its per-minute API rows, the network keys of its sites, the open
weeks of its weekly active accounts (the keyed hashes and their key), the
read state of its on-chain ids and its signed usage reports
(`docs/runbooks/tenant-removal.md`). Hourly and daily API rows, web ingest
counters, web aggregates, on-chain daily counts, calls per contract function
and closed weekly counts are deleted too, unless the operator setting
`removal_keep_aggregates` is on, in which case those aggregate rows are kept. The data is not returned in
another format: the tenant can read its own dashboards before it leaves.

## 10. Information and audits (Art. 28(3)(h))

The operator makes available the documents in `docs/compliance/` and the
source code of the whole service (the repository `github.com/justmert/veristar`), and allows for and
contributes to audits by the tenant or an auditor it mandates, on reasonable
notice.

## 11. Fees

The service and these terms carry no fee for the tenant, SCF or anyone else.

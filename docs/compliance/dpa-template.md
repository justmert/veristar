# Data processing terms (template)

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-12. These terms follow GDPR Art. 28(3) clause by clause (S10).
> They are part of the terms of use (`terms.md`, section 1) that every project
> accepts at enrollment. The operator's legal name and contact are added when
> the operating entity is decided.

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
the erasure requests it makes. The operator processes personal data only on
these instructions, including with regard to transfers to third countries,
unless EU or member state law requires otherwise, in which case it informs the
tenant first unless that law forbids it. The operator informs the tenant
immediately if, in its opinion, an instruction infringes data protection law.

## 4. Confidentiality (Art. 28(3)(b))

Everyone the operator authorises to process the data is bound to
confidentiality. Production access is limited to the people who hold the
deployment's secrets (plan an operator input.

## 5. Security (Art. 28(3)(c), Art. 32)

The operator takes the measures in `dpia.md` section 5.

## 6. Sub-processors (Art. 28(2) and (4), Art. 28(3)(d))

The tenant gives general authorisation for the sub-processors in
`subprocessors.md`. The operator announces an intended addition or replacement
in the repository's changelog and in the console before it takes effect, so
the tenant can object; if the objection cannot be resolved, the tenant may
leave the service and have its data deleted under clause 9. The length of the
notice period is an open item for the legal review an operator input. The operator imposes on each sub-processor the same
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
data (Umami websites with their events and sessions), revokes its OTLP tokens
and deletes its per-minute API rows (`docs/runbooks/tenant-removal.md`).
Hourly and daily API rows, web ingest counters and web aggregates are deleted
too, unless the operator setting `removal_keep_aggregates` is on, in which
case those aggregate rows are kept. The data is not returned in
another format: the tenant can read its own dashboards before it leaves.

## 10. Information and audits (Art. 28(3)(h))

The operator makes available the documents in `docs/compliance/` and the
source code of the whole service (the repository, public from this stage on), and allows for and
contributes to audits by the tenant or an auditor it mandates, on reasonable
notice.

## 11. Fees

The service and these terms carry no fee for the tenant, SCF or anyone else.

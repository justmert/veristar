---
title: "SCF awards"
description: "Import an SCF awards file so enrolled projects get their cohort proposed, and the enrollment form finds a project's award."
icon: "award"
---

You can import an SCF awards file with the command line. Veristar then uses it in two places:

- **Enrollment.** A person enrolling a project searches the awards by project name or website and
  picks the project's award. The form fills the cohort, the website and the Stellar ids, and the
  cohort arrives as the project's own proposal.
- **Cohort proposals.** Veristar matches every award to the enrolled projects on exact evidence
  and proposes the cohort of a matched project that has none. You confirm or dismiss it in the
  **SCF cohort** panel of the project's page under **All projects**, with **Confirm proposal** or
  **Dismiss proposal**, as any proposal ([global-views.md](global-views.md)).

Apart from that search, which answers only the fields the form fills, no page or route lists the awards.

## What the import keeps

The import keeps organisation data and drops everything else while it reads the file, so no
personal field reaches the database or the logs.

| Kept | From the file's column |
|---|---|
| Project name | project |
| Round, written `SCF #<n>` | round |
| Category | category, through the table below |
| Program | award type: `Build` becomes **Build Award** |
| Award date | award date, `M/D/YYYY` or `YYYY-MM-DD`; empty is allowed |
| Award amount in US dollars | award amount, such as `$100,000.00` |
| Tranche percent and status | tranche percent (0 to 100) and status |
| Website host | website, reduced to its host: lower case, without `www.` and without a trailing dot |
| Stellar ids | every account (`G...`) and contract (`C...`) id with a valid checksum anywhere in the row; only the ids, never the text around them |

Dropped: names of people, LinkedIn and X profiles, images, document and DocSend links, videos,
descriptions, traction and deliverable text, submission links and paid amounts.

### Categories

Each SCF category keeps its SCF name as the cohort category. A category outside this table
rejects the row, so a new category enters only after it is added here.

| SCF category | Cohort category |
|---|---|
| Applications | Applications |
| End-User Application | End-User Application |
| Financial Protocols | Financial Protocols |
| Developer Tooling | Developer Tooling |
| Infrastructure & Services | Infrastructure & Services |

## Import a file

The import runs with `platformctl` on the core node that holds the Patroni leader, with the flags
of [bootstrap step 10](../deploy/bootstrap.md). It reads the file from standard input, so the
file streams from your machine over SSH and never rests on a server:

```sh
ssh <core node> sudo docker run --rm -i --network veristar-core_default \
  --env-file /etc/veristar/env/platformctl.env \
  -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
  -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
  -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
  ghcr.io/justmert/veristar/platformctl:<release> \
  scf import - < awards.tsv
```

Run `scf import -dry-run -` first: it checks the file and prints the counts without touching the
database.

The import reads tab and comma separated files, with or without a header row. With a header row,
it finds the columns by name (`Project`, `Round`, `Category`, `Award Type`, `Award Amount`, and
optionally `Award Date`, `Website`, `Tranche %` and `Status`); without one, it reads the columns
at fixed positions. Fields quoted over several lines are read as one field.

The import prints counts: records read, awards inserted, updated and unchanged, rows rejected,
awards and projects matched, ambiguous awards, awards with no enrolled project, proposals written,
and matched projects per cohort state. Every rejected row prints as
`rejected record=<n> line=<line> field=<field> reason=<reason>`, without its values. The reasons
are `missing`, `invalid` (a date, an amount or a website that cannot be read), `unknown_value` (a
category or an award type outside the tables above), `out_of_range` (an award date before 2018 or
after today, a tranche above 100), `too_few_columns`, `duplicate_project_round` and
`too_many_stellar_ids` (more than 50).

The import upserts by project and round: a later file updates the awards it repeats and adds the
new ones, and an award missing from a later file stays. Importing the same file twice changes
nothing and writes no second proposal. Every run is written to the audit log as `scf.import`.

## How awards match projects

An award matches an enrolled project (pending, active or suspended) only on exact evidence:

- its website host is the host of one of the project's sites (the domain or an origin) or of the
  website the project gave at enrollment, compared in the form above; or
- one of its Stellar ids is an on-chain id the project verified.

Names are never compared: an award and a project that share only a name stay unmatched. A host
that many organisations share is never evidence: `github.com`, `gitlab.com`, `bitbucket.org`,
Google Docs, Drive and Sites, `communityfund.stellar.org`, `stellar.org`, Medium, Notion,
Linktree, X, Twitter, LinkedIn, YouTube, Telegram, Discord, Figma, DocSend and Loom.

A project funded in several rounds matches all its awards, and its proposal comes from the most
recent one (latest award date, then highest round). An award that points to more than one
project matches nothing, and neither does any other award of those projects; set their cohorts
yourself.

## Cohort proposals

The matching never overwrites a decision:

- a confirmed cohort stays;
- a project's own proposal stays, including one it picked with the award search;
- a proposal you dismissed is never proposed again from the same award.

A pending proposal follows a later change of its award, and moves to a newer award when a later
import adds one. A new project gets its proposal when it enrolls or adds a site whose host matches
an award. When a project proves an on-chain id later, run `platformctl scf match` (the same
command with `scf match` in place of `scf import -`) to propose its cohort without importing
again.

The routes are sections 4.2.1, 4.2.7 and 19.5 of the
[control plane API reference](../reference/control-plane-api.md).

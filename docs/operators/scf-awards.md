---
title: "SCF awards"
description: "Import SCF's build award export, see which funded projects Veristar measures, and fill in project cohorts from exact matches."
---

The SCF award directory answers one question: which funded projects does Veristar measure? You
import SCF's build award export, Veristar matches every award to the enrolled projects on exact
evidence, proposes each matched project's cohort from its award, and lists every award with the
project it matched and whether that project has data.

## What the import keeps

The export holds organisation data next to personal data. The import keeps the organisation data
and drops everything else while it reads the file, so no personal field reaches the database or
the logs.

| Kept | From the export's column |
|---|---|
| Project name | project |
| Round, written `SCF #<n>` | round (a trailing space is dropped) |
| Category | category, through the table below |
| Program | award type: `Build` becomes **Build Award** |
| Award date | award date, `M/D/YYYY` or `YYYY-MM-DD`; empty is allowed |
| Award amount in US dollars | award amount, such as `$100,000.00` |
| Tranche percent and status | tranche percent (0 to 100) and status, such as `Pre-Launch #3 - Mainnet` |
| Website host | website, reduced to its host: lower case, without `www.` and without a trailing dot |
| Stellar ids | every account (`G...`) and contract (`C...`) id with a valid checksum anywhere in the row; only the ids, never the text around them |

Dropped: names of people, LinkedIn and X profiles, images, document and DocSend links, demo
videos, descriptions, traction and deliverable text, the submission link and the paid amounts.

### Categories

Operators compare projects by the category SCF gives them, so each SCF category keeps its SCF
name. A category outside this table rejects the row, so a new SCF category enters the directory
only after it is added here.

| SCF category | Cohort category |
|---|---|
| Applications | Applications |
| End-User Application | End-User Application |
| Financial Protocols | Financial Protocols |
| Developer Tooling | Developer Tooling |
| Infrastructure & Services | Infrastructure & Services |

## Import an export

The import runs with `platformctl` on the core node that holds the Patroni leader, with the flags
of [bootstrap step 10](../deploy/bootstrap.md). It reads the export from standard input, so the
file streams from your machine over SSH and never rests on a server:

```sh
ssh <core node> sudo docker run --rm -i --network veristar-core_default \
  --env-file /etc/veristar/env/platformctl.env \
  -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
  -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
  -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
  ghcr.io/justmert/veristar/platformctl:<release> \
  scf import - < scf-awards.tsv
```

Run it with `scf import -dry-run -` first: the dry run reads and checks the file and prints the
counts without touching the database.

The import reads tab and comma separated exports, with or without a header row. With a header
row, it finds the columns by name (`Project`, `Round`, `Category`, `Award Type`, `Award Amount`,
and optionally `Award Date`, `Website`, `Tranche %` and `Status`). Without one, it reads the
column layout of SCF's export. Fields quoted over several lines are read as one field.

The import prints what it did:

```text
import_id=0193...
records=390
inserted=12
updated=3
unchanged=375
rejected=0
matched=4
ambiguous=1
not_enrolled=385
proposals_written=2
cohort_proposed=2
cohort_confirmed=2
ambiguous award=0193... round="SCF #44" project="Example" tenants=example-a,example-b shared_with=0
```

Every rejected row prints as `rejected record=<n> line=<line> field=<field> reason=<reason>`,
without its values. The reasons are `missing`, `invalid` (a date, an amount or a website that
cannot be read), `unknown_value` (a category or an award type outside the tables above),
`out_of_range` (an award date before 2018 or after today, a tranche above 100),
`too_few_columns`, `duplicate_project_round` and `too_many_stellar_ids` (more than 50).

The import upserts by project and round: a later export updates the awards it repeats and adds
the new ones, and an award missing from a later export stays. Importing the same file twice
changes nothing and writes no second proposal. Every run is recorded with the file's SHA-256 and
its counts, and written to the audit log as `scf.import`.

## How awards match projects

An award matches an enrolled project (pending, active or suspended) only on exact evidence:

- its website host is the host of one of the project's sites (the domain or an origin) or of the
  website the project gave at enrollment, compared in the same form as above; or
- one of its Stellar ids is an on-chain id the project verified.

Names are never compared: an award and a project that share only a name stay unmatched. A host that many
organisations share is never evidence: `github.com`, `gitlab.com`, `bitbucket.org`, Google Docs,
Drive and Sites, `communityfund.stellar.org`, `stellar.org`, Medium, Notion, Linktree, X, Twitter,
LinkedIn, YouTube, Telegram, Discord, Figma, DocSend and Loom.

An award that points to more than one project, or to a project that another award also points to
(one project funded in two rounds, for example), is **ambiguous** and matches nothing. The list
shows its candidates so you can set the cohort yourself.

## Cohort proposals

For every matched project, the import writes the award's program, round, category and award date
as the project's cohort proposal, marked as coming from the SCF import. You confirm or dismiss it
on the project's detail page, as any proposal ([global-views.md](global-views.md)). The audit log
names the source of every proposal, confirmation and dismissal.

The import never overwrites a decision:

- a confirmed cohort stays; when it contradicts the award, the award shows **confirmed differs**;
- the project's own proposal stays; when it contradicts the award, the award shows **member
  proposal differs**;
- a proposal you dismissed is never proposed again from the same award.

A new project gets its proposal the moment it enrolls or adds a site whose host matches an award.
When a project proves an on-chain id later, run `platformctl scf match` (the same command with
`scf match` in place of `scf import -`) to propose its cohort without importing again.

## Which funded projects are measured

`GET /api/operator/scf/awards` lists every award with:

- its match: the project, the evidence (site domain, site origin, enrollment website or on-chain
  id, and whether the site's domain is verified), or the candidates of an ambiguous award;
- for a matched award, whether Veristar measures the project over the range (web page views, API
  requests or on-chain operations), its sites and verified ids, and its last day with data;
- the project's cohort beside the award, and its state;
- a summary of awards, matches and measured projects by round and by category.

Filter it by match state, round, category, project name or measured (measured shows the matched
awards whose project has data; not measured shows every other award). The same rows download as
CSV from `GET /api/operator/scf/awards.csv`, and the operator MCP tool `list_scf_awards` reads the
list ([mcp.md](mcp.md)). The routes, fields and states are section 19.5 of the
[control plane API reference](../reference/control-plane-api.md).

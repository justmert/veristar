---
title: "Operator guide"
sidebarTitle: "Overview"
description: "For the SCF operators who govern the shared service: enrollment, removal and cross-project views."
---

Operators are the SCF people who govern the shared service: they decide who may enroll, which
projects stay, and how projects compare. Operators see aggregate numbers, plus each project's
enrollment answers, members and audit log for review. They are not members of any project and
have no access to a project's own web dashboard, whose totals come from the same daily
aggregates the operator views read.

| Page | Covers |
|---|---|
| [review-and-enrollment.md](review-and-enrollment.md) | The review queue, approving and rejecting projects and sites, suspending and resuming, and the switch between open and approval-required enrollment |
| [removal.md](removal.md) | Removing a project that is not part of the ecosystem, what is deleted, and the setting that keeps or deletes its aggregates |
| [global-views.md](global-views.md) | The Web and API views of Projects and the Compare page: ranges, filters, sorting, growth, cohorts, percentiles and side-by-side comparison |
| [threshold-rules.md](threshold-rules.md) | Saved traction rules and the projects that meet them, with the evidence |
| [scf-awards.md](scf-awards.md) | Importing an SCF awards file for the enrollment form's award search and for cohort proposals from exact matches |
| [onchain-and-exports.md](onchain-and-exports.md) | Ledger activity of the accounts and contracts projects declare, calls per contract function, and the CSV exports |
| [reading-the-numbers.md](reading-the-numbers.md) | What bands, daily-unique visitors and span-derived API data mean, and how to compare projects fairly |
| [api-keys.md](api-keys.md) | Keys for machine consumers of the metrics API: create, use, revoke, usage |
| [mcp.md](mcp.md) | Connecting an AI client to the operator view through the MCP server |
| [roster.md](roster.md) | The Operators page: roles, adding and removing operators, passkeys, and lockout recovery |
| [alerts.md](alerts.md) | The Alerts page: the platform's live alerts, the count badge and critical banner, and what the page shows when alerting itself is broken |
| [umami-admin-access.md](umami-admin-access.md) | For platform administrators with SSH access: opening Umami's own interface over a tunnel to debug the engine |

## Becoming an operator

An operator is a person on the operator roster, with a GitHub account, a Stellar wallet or a
passkey account. Operator admins add and remove operators on the **Operators** page; [roster.md](roster.md) covers the page
and the passkeys every operator registers.

- The identities in the deploy settings `OPERATOR_GITHUB_IDS` and `OPERATOR_STELLAR_ADDRESSES` are
  operator admins at every start of the control plane. They are how the first operators get in,
  and how the hosting party restores access after a lockout
  ([Adding and removing operators](../runbooks/operators.md)).
- The operator area asks for your passkey when you open it, and again after 12 hours.
- Operators see an **Operator** area in the console. Anyone else who opens `/operator` sees
  "Operators only", and the control plane answers every operator route with
  `403 operator_required`, whatever the console shows.
- A suspended or removed operator loses the operator view at their next request.

## The operator area

The operator area is at `https://app.<BASE_DOMAIN>/operator`. Its pages are in the sidebar's
Operator group, in two parts:

| Part | Page | Address | What it does |
|---|---|---|---|
| Work | Review | `/operator` | Projects waiting for approval, and active projects with a site in review, oldest first |
| Work | Alerts | `/operator/alerts` | The platform's firing alerts from Alertmanager, by severity, refreshed every 30 seconds ([alerts.md](alerts.md)) |
| Work | Projects | `/operator/projects` | Every project in three views: Status (search, status filter, sort), Web and API (numbers over a range, with filters and sort). A project opens its detail page with its actions at `/operator/projects/<id>` |
| Work | Compare | `/operator/compare` | Up to five projects side by side, web and API |
| Work | Rules | `/operator/rules` | Saved threshold rules and the projects that meet them ([threshold-rules.md](threshold-rules.md)) |
| Admin | Settings | `/operator/settings` | Enrollment mode, and whether removed projects keep their aggregates |
| Admin | Operators | `/operator/operators` | The operator roster and your passkeys ([roster.md](roster.md)) |
| Admin | Access keys | `/operator/keys` | Keys for machine access to the metrics API ([api-keys.md](api-keys.md)) and operator keys for AI clients ([mcp.md](mcp.md)) |
| Admin | Audit log | `/operator/audit` | Every recorded change, filterable by project, action, actor, target and time |

While a paging alert fires, every operator page shows one line at the top with a link to
Alerts. Press `Ctrl K` (`Cmd K` on a Mac) on any page to go to a page or a project by name.

Every operator action is written to the audit log with the operator's id. Actions that require a
reason (listed in [review-and-enrollment.md](review-and-enrollment.md)) show that reason to the
project's members.

The HTTP routes behind these pages are section 5 of the
[control plane API reference](../reference/control-plane-api.md).

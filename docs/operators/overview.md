---
title: "Operator guide"
sidebarTitle: "Overview"
description: "For the SCF operators who govern the shared service: enrollment, removal and cross-project views."
---

Operators are the SCF people who govern the shared service: they decide who may enroll, which
projects stay, and how projects compare. Operators see aggregate numbers only. They are not
members of any project and have no access to a project's own web dashboard.

| Page | Covers |
|---|---|
| [review-and-enrollment.md](review-and-enrollment.md) | The review queue, approving and rejecting projects and sites, suspending and resuming, and the switch between open and approval-required enrollment |
| [removal.md](removal.md) | Removing a project that is not part of the ecosystem, what is deleted, and the setting that keeps or deletes its aggregates |
| [global-views.md](global-views.md) | The Global web, Global API and Compare pages: ranges, filters, sorting and side-by-side comparison |
| [reading-the-numbers.md](reading-the-numbers.md) | What bands, daily-unique visitors and span-derived API data mean, and how to compare projects fairly |
| [api-keys.md](api-keys.md) | Keys for machine consumers of the metrics API: create, use, revoke, usage |
| [mcp.md](mcp.md) | Connecting an AI client to the operator view through the MCP server |
| [alerts.md](alerts.md) | The Alerts page: the platform's live alerts, the count badge and critical banner, and what the page shows when alerting itself is broken |

## Becoming an operator

An operator is a GitHub account whose numeric id is listed in the control plane setting
`OPERATOR_GITHUB_IDS`. A deployment sets it through the Ansible variable `sa_operator_github_ids`,
a comma-separated list of numbers such as `1234567,7654321`. Adding and removing ids is the
runbook [Adding and removing operators](../runbooks/operators.md).

- The control plane reads the list at start and at every sign-in. Adding an id takes effect when
  that person next signs in. Removing an id takes effect at that person's next sign-in or the next
  control plane start, whichever comes first.
- Every change of operator status is in the audit log as `user.operator_sync`.
- Operators see an **Operator** area in the console. Anyone else who opens `/operator` sees
  "Operators only", and the control plane answers every operator route with
  `403 operator_required`, whatever the console shows.

The numeric id of a GitHub account is the `id` field of `https://api.github.com/users/<login>`.

## The operator area

The operator area is at `https://app.<BASE_DOMAIN>/operator`. It has four sections in the
sidebar, each with tabs:

| Section | Tab | What it does |
|---|---|---|
| Attention | Review queue | Projects waiting for approval, and active projects with a site in review, oldest first |
| Attention | Alerts | The platform's firing alerts from Alertmanager, by severity, refreshed every 30 seconds ([alerts.md](alerts.md)) |
| Projects | Projects | Every project with search, status filter and sort; opens the project detail with its actions |
| Projects | Global web | Web numbers of every project over a range, with filters and sort |
| Projects | Global API | API numbers of every project over a range, with filters and sort |
| Compare | | Up to five projects side by side, web and API |
| Admin | Settings | Enrollment mode, and whether removed projects keep their aggregates |
| Admin | API keys | Keys for machine access to the metrics API ([api-keys.md](api-keys.md)) |
| Admin | AI / MCP | Operator keys for AI clients ([mcp.md](mcp.md)) |
| Admin | Audit log | Every recorded change, filterable by project, action, actor, target and time |

Every operator action is written to the audit log with the operator's id. Actions that require a
reason (listed in [review-and-enrollment.md](review-and-enrollment.md)) show that reason to the
project's members.

The HTTP routes behind these pages are section 5 of the
[control plane API reference](../reference/control-plane-api.md).

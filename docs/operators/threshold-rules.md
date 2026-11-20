---
title: "Threshold rules"
description: "Save a traction threshold once and list the projects that meet it, with the qualifying days and the integrity grade as evidence."
icon: "sliders"
---

A threshold rule states a traction requirement once, for example "verified visitors at or above
100 per day on at least 10 days in each of the last three months, with an integrity grade of B or
better". The console then lists every project that meets it, with the evidence. Rules are kept
for every operator, and the project sees on its **SCF review** page which rules it meets.

## A rule

| Field | Meaning |
|---|---|
| Name | what you call the rule, 1 to 100 characters |
| Metric | **Verified visitors** (primary-band visitors summed over the project's sites per UTC day), **API requests**, or **On-chain operations** (of the project's verified ids only) |
| Minimum per day | the daily value a day must reach to qualify, at least 1 |
| Days per month | how many qualifying days each month needs, 1 to 28 |
| Months | how many complete calendar months the rule looks back, 1 to 12 |
| Minimum integrity grade | optional: the integrity grade over those months must be this or better (A is best); a project without a graded day does not meet a rule with a grade |
| Cohort | optional program, round and category: only projects confirmed in it are evaluated |

The months are complete UTC calendar months before the month of the evaluation day. On
16 November with three months, the window is August, September and October; the current month
never counts, so a project's result does not change during the month.

Only active and suspended projects are evaluated. API requests follow the same rules as the
API view of **All projects**: application requests only, with infrastructure requests (CORS preflights, health
and probe routes, known health checkers) left out, and a service's metric data wins over its
span-derived data.

## Create, edit and delete

Open **Rules** in the operator area. **New rule** opens the form and **Save rule** saves it; a saved rule is edited or deleted
from the **Actions** menu of its row, and a deletion is confirmed with **Delete rule**. Every creation, change and deletion is in the audit log with the rule's
fields (old and new values for a change). At most 200 rules are kept.

## Projects that meet a rule

Choose a rule to see the projects that meet it today, or pick an earlier evaluation day. For each
project the page shows:

- its cohort and its integrity grade over the window, with the graded days per grade;
- every month of the window with its number of qualifying days;
- every qualifying day with the project's value that day.

**Export CSV** downloads one row per project and month with the qualifying days as a list; the
export is in the audit log.

## For machines and AI clients

The machine metrics API serves the same reads at `GET /api/metrics/v1/rules` and
`GET /api/metrics/v1/rules/{rule_id}/matches` ([api-keys.md](api-keys.md)); a key scoped to one
project evaluates that project only. The operator MCP tools `list_threshold_rules` and
`get_threshold_rule_matches` answer the same, and `list_cohorts` lists the cohorts in use for a
rule's cohort filter ([mcp.md](mcp.md)). The routes are section 19.4 of
the [control plane API reference](../reference/control-plane-api.md).

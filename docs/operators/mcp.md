---
title: "AI assistants for operators (MCP)"
sidebarTitle: "AI assistants (MCP)"
description: "Give an AI client the global view: search projects, rank them and compare them, read-only."
---

The MCP server at `https://app.<BASE_DOMAIN>/mcp` (production: `https://app.veristar.app/mcp`)
also serves the operator view. An operator credential gives an AI client the same daily
aggregates as the **Global web**, **Global API** and **Compare** pages, and the project list and
detail of the review pages. It reads and never acts: no tool approves, suspends or removes
anything. Projects connect with the [project guide](../onboarding/mcp.md); the protocol details
are section 12 of the [control plane API reference](../reference/control-plane-api.md).

## Getting a credential

- **Operator key.** Open **Operator**, then **Admin**, **AI / MCP**, and create a key. It acts as you, is shown
  once, and expires after 90 days by default (at most 365). Any operator can revoke any operator
  key on that page.
- **Connector.** Add the server URL to claude.ai, Claude Desktop, ChatGPT or Cursor as for a
  project. On the consent page, operators see an extra choice, **The operator view of every
  project**. Choose it instead of a project.

Either stops working when you lose operator rights: when your GitHub id leaves
`OPERATOR_GITHUB_IDS` and the control plane restarts, or when your operator flag is turned off
at your next sign-in. Your operator credentials are then revoked for good. A project credential
of an operator reads that one project only and never the operator tools.

## Tools

| Tool | Same data as |
|---|---|
| `list_tenants` | the Projects page: search by name, slug, website or site domain; filter by status or review |
| `get_tenant` | a project's detail page: status and reason, enrollment answers, members, sites with verification and stellar.toml results, removal progress |
| `get_global_web_overview` | Global web: page views, visitors, visits, bounce rate, visit duration, quarantined traffic, gateway rejections and top referrers, countries, devices and browsers per project, sortable and filtered |
| `get_global_api_overview` | Global API: requests, 4xx and 5xx rates, p50, p95 and p99, services and routes per project |
| `compare_tenants_web`, `compare_tenants_api` | Compare: up to five projects side by side with a point per UTC day |

Ranges are whole UTC days, both included, at most 366 days, the 30 days ending today by default.
Sessions and raw events are never part of the operator view.

## Example questions

- "Which ten projects had the most visitors in the last 30 days, and which had none?"
- "List projects in review, and those with a verified domain but no web traffic."
- "Compare the API traffic of these three projects over the last quarter, with their busiest
  services."
- "Which projects have a high share of quarantined or rejected web events?"
- "Find the project with the site domain example.org and show its enrollment answers."

## Audit and limits

Every tool call writes an audit row (`mcp.tool_call`) with the tool, the credential, the user, the
duration and the outcome, never the answer; the **Audit log** page shows them. Each credential may
make 5 requests per second (burst 30) per control plane instance and run 4 calls at once. To
revoke a key or connection that should not exist, or to investigate failing calls, follow the
runbook [MCP access](../runbooks/mcp-access.md).

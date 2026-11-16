---
title: "0005: Umami as the engine, the dashboard in the console"
sidebarTitle: "0005: Umami as the engine"
description: "Umami stays the ingestion target, the store and the metric computation; its interface is closed to projects and the web dashboard is a console page."
---

- Status: accepted
- Date: 2026-11-16
- Supersedes: [ADR 0004](0004-umami-dashboard-theme.md)

## Context

Until this decision a project read its web analytics in Umami's own interface on
`web.<BASE_DOMAIN>`, reached from the console by a single sign-on redirect and shown in a
Veristar theme (ADR 0004). That arrangement had four costs:

- Two products: the console for setup, API metrics, the integrity grade and the on-chain
  panel, and a second site for web numbers, with its own navigation and look.
- An internet-facing Next.js application with its own routes to deny, a session token that
  travelled in a URL, and buttons that view-only members could press and that then failed.
- A theme that depended on Umami's class names and custom properties, which every Umami
  upgrade had to recheck before the 24 hour patch target could be met.
- Numbers that the operator views and the project could read from two places, computed in
  two ways.

The RFP asks for a platform "built upon either Umami or GoatCounter" (section 2), for a
private tenant dashboard with page views, unique visitors, referrers, session duration,
bounce rate, country, device and browser for selectable time ranges (section 3.2), and for
strict isolation (section 3.1).

## Decision

- Umami stays the engine: the official image, unmodified and pinned by digest
  ([ADR 0001](0001-base-platform.md), [ADR 0002](0002-overlay-not-fork.md)). The replayer
  stores every accepted event through its `/api/send`; its tables hold the raw events and
  sessions; its `/stats` and `/metrics` queries compute the metrics.
- Umami's interface is closed to projects. No public host serves it: the `web.` hostname,
  its DNS record, its Traffic Manager profile, its Caddy site and its certificate are
  removed, and no Caddy site proxies to Umami.
- The web dashboard is a console page (**Web** in each project) over the control plane
  routes of [control-plane-api.md](../reference/control-plane-api.md) section 18. Totals,
  days and the referrer, country, browser, OS and device breakdowns come from the daily
  aggregates the operator views read; pages, custom events, UTM values, hours and the
  visitors right now come from Umami's API on the private network.
- Umami is read as the member's own Umami user. The per-member Umami users, one team per
  project, `view-only` roles and the daily derived passwords stay. The alternative, one
  service account with all scoping in the control plane, would leave a single check
  between a project and another project's numbers. Reading as the member keeps Umami's own
  team check as a second, independent gate: a fault in the control plane's site check
  still meets Umami's refusal. The isolation gate tests both gates (`make isolation`).
- The daily password rotation (`umami-rotate`) stays, because these logins use it.
- Platform administrators who need Umami's interface for debugging reach it over an SSH
  tunnel to the node's loopback (Umami process 1 publishes `127.0.0.1:3300`), never through
  a public host ([operators/umami-admin-access.md](../operators/umami-admin-access.md)).
- The Veristar theme of Umami's interface and its tests are removed. This decision
  supersedes [ADR 0004](0004-umami-dashboard-theme.md).

## Consequences

Good:

- One product: a project sees web, API, integrity and on-chain figures in one console.
- A project and an operator see the same web totals for the same days, because both read
  `agg.web_daily` and `agg.web_daily_dim`. `TestWebDashboardParity` checks every figure the
  dashboard shows against Umami's own answer for the same websites and range.
- Umami's Next.js application, its routes and its session tokens are off the internet. A
  vulnerability that needs a request to Umami from the internet, including one through
  share tokens, has no path to it.
- An Umami upgrade no longer reviews a theme; it reviews the API and data contract
  ([umami-contract.md](../reference/umami-contract.md)).
- The dashboard availability probe watches the console and the machine API only.

Costs:

- Umami's report pages (funnels, journeys, retention, goals, session lists) are not in the
  console. The MCP tools still read them as the member (control-plane-api.md section 12).
- Engine reads cost Umami a query per site, so they are limited per member (120 a minute).
- Today's totals follow the hourly aggregation; the hours of today and the visitors right
  now are read live.
- An administrator who opens Umami's interface over the tunnel logs in with the admin
  credentials and sees every project's raw data. That access already exists through the
  database on the same node; the tunnel adds no new holder.

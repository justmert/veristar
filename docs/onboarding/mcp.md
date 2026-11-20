---
title: "AI assistants (MCP)"
sidebarTitle: "AI assistants (MCP)"
description: "Ask Claude, ChatGPT, Cursor or any MCP client about your project's web and API analytics."
icon: "robot"
---

Veristar runs a [Model Context Protocol](https://modelcontextprotocol.io) server at
`https://app.analytics.example/mcp` (your deployment's console address followed by `/mcp`; the
project's **AI assistants** page shows it as **Server URL** under **Connect a client**). Connect an AI client to it and ask questions in plain
language: the client calls read-only tools that return your project's web analytics (from the
tracker), API analytics (from OpenTelemetry) and on-chain activity. The contract is section 12 of
[`docs/reference/control-plane-api.md`](../reference/control-plane-api.md).

## What a connection can do

- It reads **one project**: the one it was made for. Every tool is read-only; nothing can be
  changed through MCP.
- It acts as **you**: the person who made the key or approved the connector. It works while you
  are a member of the project and stops at once when you leave it, when an owner removes you, or
  when the project is suspended, not approved or removed. Revoking it on the project's
  **AI assistants** page stops it on the next request.
- It sees what the console shows you and more of your own sites' data: page views, visitors,
  visits, bounce rate, visit duration, referrers, channels, countries, devices, browsers, custom
  events and their data, visitor sessions, funnels, journeys, retention and goals; your API's
  requests, status codes, error rates and latency percentiles per service and route; and your
  accounts' and contracts' daily ledger activity, weekly active accounts and calls per contract
  function.

## Two ways to connect

| | Access key | Connector (OAuth) |
|---|---|---|
| Clients | Claude Code, Cursor, any client that sends a header | claude.ai, Claude Desktop, ChatGPT, Claude Code, Cursor |
| Made on | **Create an access key** on the project's **AI assistants** page | the consent page the client opens |
| Lifetime | 90 days by default, at most 365 | access token one hour, refreshed by the client; the connection lasts until revoked while the client refreshes it at least every 30 days |
| Secret | `sa_mcp_` and 40 characters, shown once | handled by the client |

Both appear under **Access keys and connections** on the **AI assistants** page with when they
were last used. Members can revoke
their own; owners can revoke any of the project's.

### Claude Code

With an access key from the **AI assistants** page:

```sh
claude mcp add --transport http veristar https://app.analytics.example/mcp \
  --header "Authorization: Bearer sa_mcp_..."
```

Or without a key: run `claude mcp add --transport http veristar https://app.analytics.example/mcp`,
then `/mcp` in Claude Code and choose to authenticate. Your browser opens the consent page.

### claude.ai and Claude Desktop

Open **Settings**, **Connectors**, **Add custom connector**. Paste
`https://app.analytics.example/mcp` as the URL and leave the OAuth client id and secret empty. Claude
registers itself and opens the consent page; you sign in, choose the project and approve.

### ChatGPT

In **Settings**, **Connectors**, create a connector with the URL
`https://app.analytics.example/mcp` and OAuth authentication. ChatGPT registers itself and opens the
consent page, as above.

### Cursor

Add to `.cursor/mcp.json` in a repository, or to `~/.cursor/mcp.json` for every project:

{/* include examples/mcp-clients/cursor-mcp.json */}
```json
{
  "mcpServers": {
    "veristar": {
      "url": "https://app.analytics.example/mcp",
      "headers": {
        "Authorization": "Bearer sa_mcp_..."
      }
    }
  }
}
```

Leave out `headers` and Cursor uses the OAuth flow instead.

## The consent page

A connector sends your browser to `https://app.analytics.example/ai/consent`. The page asks you to
sign in when needed (GitHub, a Stellar wallet or a passkey, see [Signing in](sign-in.md)), then shows the client's name, where your browser returns afterwards, and
your projects under **Give access to**. Pick one and choose **Approve**; **Deny** sends the client
away without access. Only projects
that are active or in review can be chosen.

## Example questions

- "How many visitors did the site have last week compared with the week before?"
- "Which pages and referrers brought the most traffic this month?"
- "Which countries and devices do the visitors use?"
- "How many people are on the site right now?"
- "How often was the `connect-wallet` event fired per day, and with which wallets?"
- "Of the visitors who opened /pricing, how many reached /signup within an hour?"
- "What do visitors do after the home page?"
- "Which API routes had the most 5xx errors in the last 7 days, and what is their p95 latency?"
- "Show the API request rate per hour for the last day."
- "Are there ingest warnings for the API data?"
- "How many weekly active accounts did the project have this month?"
- "Which functions of our contract are called most, and how many of those calls fail?"

## Tools

| Area | Tools |
|---|---|
| Project | `list_projects`, `list_sites`, `get_site` |
| Web analytics | `get_web_stats`, `get_web_traffic`, `get_web_metrics`, `get_web_realtime`, `get_event_stats`, `get_event_series`, `get_event_properties`, `get_sessions`, `get_session_stats`, `list_funnels`, `run_funnel`, `run_journey`, `run_retention`, `get_goals` |
| API analytics | `list_api_services`, `get_api_overview`, `get_api_timeseries`, `get_api_status_codes`, `list_api_routes`, `get_api_top_error_routes`, `get_api_warnings` |
| On-chain | `get_onchain_activity`, `get_contract_functions` |
| Integrity and review | `get_web_integrity`: your web traffic's integrity grade per site and day with its reasons, the same report SCF operators see; `get_scf_view`: your project as SCF reviewers see it |

Web tools take a `site_id` from `list_sites` and an optional `view`: `primary` (accepted traffic)
or `quarantine` (events the gateway flagged as likely automated). Dates are ISO 8601, such as
`2026-11-01` or `2026-11-01T09:00:00Z`; web ranges cover at most 366 days.

## Reading the answers

- Unique visitors use a daily rotating visitor token and no cookie, so visitors over several days
  are the sum of daily visitors, and retention shows returns within what that allows. See
  [`docs/operators/reading-the-numbers.md`](../operators/reading-the-numbers.md).
- API latency percentiles are estimated from histogram buckets, never from single requests; the
  answers say so, and a value of `">10 s"` means the percentile lies above the last bucket.
- UTM values appear only for sites whose owner turned on UTM collection.

## Limits

Each credential can make 5 requests per second (burst 30) on each control plane instance and run 4
tool calls at once. One call runs for at most 30 seconds and returns at most 512 KiB. For a larger
question, use a shorter range, a filter or a lower `limit`.

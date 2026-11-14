---
title: "MCP access: revoke and investigate"
sidebarTitle: "MCP access"
description: "Find and revoke an MCP access key or connector, review what it did, and investigate failing tool calls."
---

AI clients read analytics through the MCP server at `/mcp` with an access key (`sa_mcp_` plus 40
characters) or an OAuth connection approved on the consent page. Both act as one console user and
are rows of `cp.mcp_credentials`; only hashes are stored. Nothing caches a credential: a
revocation applies to the next request. Contract:
[../reference/control-plane-api.md](../reference/control-plane-api.md) section 12. Guides:
[../onboarding/mcp.md](../onboarding/mcp.md) and [../operators/mcp.md](../operators/mcp.md).

## What revokes on its own

Removing a member, suspending, rejecting or removing a project, and turning off a user's operator
flag revoke the affected credentials in the same transaction (migration 00030), with an audit row
`mcp_credential.revoke` from `system:mcp-revoke`. A refresh token or authorization code used twice
revokes its connection. No step below is needed for these.

## Revoking a credential

1. **Find it.** A project's members see their keys and connections on the project's **AI / MCP**
   page; operators see operator credentials under **Operator**, **AI / MCP**. A leaked key is
   identified by its prefix (`sa_mcp_` and four more characters). To find a credential by prefix
   across projects, read the database as the platform owner on the leader:

   ```sql
   select id, kind, scope, tenant_id, user_id, name, created_at, last_seen_at, revoked_at
   from cp.mcp_credentials where prefix = 'sa_mcp_Ab3x';
   ```

2. **Revoke it.** The project's owner, or the member who made it, uses **Revoke** on the AI page
   (`DELETE /api/tenants/<tenant id>/mcp-credentials/<id>`). An operator credential is revoked
   under **Operator**, **AI / MCP** (`DELETE /api/operator/mcp-credentials/<id>`). For a project
   credential whose owner cannot be reached, an operator suspends the project
   ([tenant-removal.md](tenant-removal.md) lists the lifecycle actions), which revokes every
   credential of the project; resuming does not bring them back.

3. **Confirm.** A request with the key answers `401` with `error="invalid_token"`. The
   credential shows `revoked` with its reason.

## Reviewing what a credential did

Every tool call is an audit row with action `mcp.tool_call` and actor
`mcp_credential:<id>`. The **Audit log** page filters by project; in the database:

```sql
select at, detail->>'tool' as tool, detail->>'outcome' as outcome, detail->>'duration_ms' as ms
from cp.audit_log
where action = 'mcp.tool_call' and actor = 'mcp_credential:<id>'
order by at desc limit 200;
```

The rows hold the tool name, outcome, duration, user and project, never the arguments or the
answer. A key that leaked and is still used after revocation shows up as `401` answers in
`controlplane_mcp_requests_total{outcome="unauthenticated"}`, not in the audit log.

## Investigating failing calls

`MCPToolFailuresHigh` ([alerts.md](alerts.md)) fires when server-side failures dominate.

1. `sudo docker compose logs controlplane | grep "mcp tool failed"` names the tool and the error.
2. `unavailable` outcomes come from Umami or the database: check `UmamiHeartbeatFailing` and the
   database alerts first. Web tools log in to Umami as the project member's Umami user; a member
   whose Umami user is not provisioned yet gets `provisioning_pending`, which is not a failure.
3. `timeout` outcomes are calls longer than 30 seconds, usually wide ranges on large sites. They
   are client behaviour unless they come with database or Umami latency.
4. `internal` outcomes are bugs: open an issue with the log lines (they hold no credential).

## Rate limits and abuse

Each credential may make 5 requests per second (burst 30) per control plane instance and run 4
tool calls at once; registration of OAuth clients is limited to 10 a minute per instance, and
clients that never complete a grant are deleted after a day. A client that keeps hitting the
limits shows in `controlplane_mcp_requests_total{outcome="rate_limited"}`; revoke its credential
if it should not run.

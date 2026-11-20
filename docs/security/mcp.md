---
title: "AI clients and the MCP server"
sidebarTitle: "AI clients (MCP)"
description: "How the MCP server authenticates AI clients, keeps tenants apart and limits what a client receives."
icon: "robot"
---

The control plane serves a Model Context Protocol endpoint at `https://app.<BASE_DOMAIN>/mcp`
(contract: section 12 of [`docs/reference/control-plane-api.md`](../reference/control-plane-api.md)).
This page states its security model; the privacy side is in
[`docs/compliance/privacy-strategy.md`](../compliance/privacy-strategy.md), "AI clients".

## Read-only by construction

- Every tool is read-only. Project tools call the console's own read routes in process, behind a
  router that passes `GET` only, so no tool can reach a write route even through a bug.
- Web tools call Umami through an allowlist of read routes of one website
  (`internal/umami/read.go`); any other path is refused before a request is made.
- Umami's own MCP endpoint stays disabled (`MCP_ENABLED` unset), and no public host serves
  Umami, so neither it nor Umami's API key routes are reachable from the internet.
  Their keys would live in Umami, outside the revocation rules below.

## Credentials

| | Access key | OAuth connection |
|---|---|---|
| Format | `sa_mcp_` and 40 random base62 characters (about 238 bits) | access token `sa_mcpat_`, refresh token `sa_mcprt_`, each with 40 random characters |
| Stored | SHA-256 and a four-character display prefix | SHA-256 only |
| Lifetime | 1 to 365 days, 90 by default | access 1 hour; refresh 30 days, rotated on every use |
| Made by | a project member on the AI page, or an operator | the person on the consent page, signed in to the console with GitHub, a Stellar wallet or a passkey |

- A credential acts as one console user. Every request reads the credential, the user, the
  membership and the project status again in one statement; nothing is cached.
- Revocation is automatic when a member is removed, a project is suspended, rejected or removed,
  or an operator flag is turned off (database triggers, migration 00030), and manual on the AI
  pages. It is final: restoring the project or the flag does not restore a credential.
- Tokens are audience-bound: a credential carries the resource URI it was issued for
  (RFC 8707) and is refused by a server with another one.
- A refresh token or authorization code presented twice revokes the whole connection (RFC 9700
  4.14.2, RFC 6749 4.1.2).
- Lookups go by the hash of the presented value and the stored hash is compared again in
  constant time; a malformed value never reaches the database.

## The OAuth flow

The control plane is its own authorization server (RFC 8414) and protected resource (RFC 9728).
Clients register dynamically (RFC 7591) as public clients without a secret, and every
authorization uses PKCE with S256; `plain` and missing challenges are refused. Redirect URIs must
be registered exactly: `https`, `http` on a loopback host, or a native app's private-use scheme;
`javascript:`, `data:`, `file:` and similar are refused. Errors before the client and redirect URI
are verified are shown on the console page, never redirected. The authorization request travels to
the consent page sealed with AES-256-GCM under a key derived from `OAUTH_STATE_KEY` with HKDF and
a label of its own, and expires after 10 minutes. The consent page needs a console session, shows
the client's name and the origin the browser returns to, and grants exactly one project (or the
operator view for operators) only on an explicit approve; the approve call carries the console's
CSRF header and Origin check, and approving the operator view also needs a fresh passkey step-up
of the operator area. The authorization answer carries `iss` (RFC 9207).

## Tenant isolation

- A project credential names one tenant. Every project tool builds its request from that tenant
  id, never from the client's input; a site id from the client is read through the tenant's own
  site route first, which answers only for a site of that tenant under row-level security. Only
  then is the Umami website id derived, so a website id from a client never reaches Umami.
- Saved funnel and goal ids are checked against the site's own list, and Umami checks again that
  the report belongs to the website.
- Operator tools exist only on operator credentials. A project credential of an operator lists
  no operator tool, and the in-process reader refuses any operator path for a project credential.
- The isolation gate (`make isolation`) runs every tool of a tenant A credential against tenant B's
  sites and ids, with the real SDK client over Streamable HTTP, and checks that nothing of B
  appears in any answer (`tests/isolation/console/mcp_test.go`, `gate_test.go`).

## Transport

- `POST /mcp` only, stateless: no session to hijack, and any instance answers.
- A request with an `Origin` header is refused (403), which closes DNS rebinding and keeps
  browser pages out. No CORS header is sent on `/mcp` or the token, registration and revocation
  endpoints; only the two public discovery documents allow any origin.
- The session cookie means nothing on `/mcp`, and an MCP credential means nothing on any other
  route.
- Caddy's access log deletes every request header, `Authorization` included, and the query.

## Limits and monitoring

5 requests per second (burst 30) and 4 running tool calls per credential and instance, 30 seconds
per call, 512 KiB per result, 64 KiB per request, 20 active keys per user and project, 10 client
registrations a minute per instance. Every tool call writes an audit row without its arguments or
result. Metrics `controlplane_mcp_*` and the alert `MCPToolFailuresHigh` cover failures; the
runbook is [`docs/runbooks/mcp-access.md`](../runbooks/mcp-access.md).

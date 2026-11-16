---
title: "Control plane HTTP API"
description: "The HTTP contract between the console and the control plane: routes, fields and error codes."
---

The contract between the console (`apps/console`) and the control plane
(`cmd/controlplane`, `internal/controlplane`). Both sides build against this file.
A change to a route, a field or an error code is made here first, in the same
commit as the code.

Database names are those of the migrations in `db/migrations/`
(`00002_cp_tables.sql` and `00003_cp_security.sql` for the core tables). Where a
response field maps to a column, the field has the column's name.

Sections 1 to 3 hold the conventions every route follows: transport,
authentication, errors, shared objects and the audit log. Sections 4 to 12 hold
the routes by area:

| Section | Area |
|---|---|
| 4 | sign-in, tenants, members, sites, snippet, ingest counts, SSO to Umami |
| 5 | operator governance: review, enrollment modes, suspension, removal, domain checks |
| 6 | OTLP tokens |
| 7 | a tenant's API metrics |
| 8 | the operators' global web and API views |
| 9 | operator API keys and the machine metrics API |
| 10 | erasure |
| 11 | operator alerts |
| 12 | the MCP server |
| 15 | the on-chain witness: declared accounts and contracts, their proofs, daily ledger activity and the two-way stellar.toml link |
| 16 | CSV exports and "What SCF sees" |
| 17 | the integrity grade of web traffic |

A new route adds its error codes to section 2.4 and its audit actions to section
3.3. The meaning of an existing field never changes.

---

## 1. Transport

| Item | Rule |
|---|---|
| Base URL | `https://app.<BASE_DOMAIN>/api`. Caddy (`deploy/caddy/app.Caddyfile`) serves the console and proxies `/api/*` to the control plane unchanged. |
| Dev base URL | `http://localhost:8085/api` (the GitHub OAuth App callback is `http://localhost:8085/api/auth/github/callback`). 8080 is the dev gateway, so the control plane listens on 8085 and is also the dev console origin. |
| Media type | Request and response bodies are `application/json; charset=utf-8`. A body on a request with another `Content-Type` is refused with 415. |
| Body size | Request bodies are limited to 64 KiB (413). |
| Unknown fields | A request body with a field this document does not list is refused with 400 `invalid_body`, so a typo never passes silently. |
| Identifiers | Lowercase UUID strings (`cp.*.id` are UUIDv7 from `uuidv7()`; site ids are UUIDv7 made by the control plane). A malformed id in a path is answered with 404 `not_found`, the same as an unknown id. |
| Times | RFC 3339 in UTC with a `Z` suffix and no fractional digits beyond milliseconds, for example `2026-11-12T09:00:00Z`. |
| Absent values | A nullable column is returned as JSON `null`, never omitted. |
| Caching | Every `/api` response carries `Cache-Control: no-store`. |
| Logs | Caddy and the control plane never log the remote address, the user agent, the cookie or a query string. |
| Ops endpoints | `/healthz`, `/readyz` and `/metrics` are served on the separate ops listener (`CONTROLPLANE_OPS_ADDR`, `internal/obs`), never under `/api`. |

### 1.1 Authentication

- **Session cookie.** Name `sa_session`. Value: 32 random bytes, base64url, made by
  `scs`. Attributes: `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, no `Domain`
  (host-only, so the cookie never reaches `web.` or `in.<BASE_DOMAIN>`). Persistent,
  absolute lifetime 7 days, idle timeout 24 hours. The database holds only the
  SHA-256 of the value (`cp.sessions.token_hash`, 32 bytes) and the user
  (`cp.sessions.user_id`). The session store is the project's own `scs` store
  (`internal/controlplane/session`), because `scs`'s `pgxstore` works only on its
  fixed table layout, which `cp.sessions` does not have.
- The token is renewed (`RenewToken`) at sign-in, so a session id fixed before
  sign-in is useless afterwards.
- **OAuth state cookie.** `sa_oauth`, only during sign-in: `HttpOnly`, `Secure`,
  `SameSite=Lax`, `Path=/api/auth/github/callback`, `Max-Age=600`. Value: AES-256-GCM
  sealed (key `OAUTH_STATE_KEY`) JSON `{state, verifier, return_to, exp}`. No database
  row is written for a visitor who has not signed in.
- **Wallet challenge cookie.** `sa_stellar`, only between a wallet challenge and
  its verification (4.1.7, 4.1.8): `HttpOnly`, `Secure`, `SameSite=Strict`,
  `Path=/api`, `Max-Age=300`. Value: AES-256-GCM sealed (key `OAUTH_STATE_KEY`,
  additional data `sa_stellar/v1`) JSON `{nonce, address, purpose, user_id,
  return_to, exp}`. Every verification clears it.
- **Passkey challenge cookie.** `sa_passkey`, only between a passkey challenge
  and its verification (4.1.9): the attributes of `sa_stellar`. Value:
  AES-256-GCM sealed (key `OAUTH_STATE_KEY`, additional data `sa_passkey/v1`)
  JSON `{challenge, purpose, user_id, return_to, exp}`. Every verification
  clears it.
- A route marked **session** answers 401 `unauthenticated` without a valid session.
  A route marked **public** needs none.

### 1.2 Cross-site request forgery

Every request with method `POST`, `PUT`, `PATCH` or `DELETE` must carry both:

1. `X-Requested-With: sa-console` (exact, case-sensitive value). Missing or different:
   403 `csrf_header_missing`. A cross-site form cannot set a custom header, and a
   cross-site `fetch` with one needs a CORS preflight, which the control plane never
   grants (it sends no `Access-Control-Allow-*` header on any `/api` route; the
   only such header is `Access-Control-Allow-Origin: *` on the two public OAuth
   discovery documents of 12.3).
2. `Origin` equal to `CONSOLE_ORIGIN` (`https://app.<BASE_DOMAIN>`; dev
   `http://localhost:8085`). Missing or different: 403 `origin_mismatch`.

`GET` and `HEAD` never change state. The only `GET` routes with side effects outside
the database are the two browser redirects (`/api/auth/github/start` and
`/api/sso/umami`), covered in their sections.

### 1.3 Tenant scoping

- Every route under `/api/tenants/{tenant_id}` and `/api/sites/{site_id}` first checks
  membership under `cp_system` (`cp.tenant_members` for the session user). A
  non-member gets **404 `not_found`**, never 403, for every such route, also for ids
  that exist. The isolation tests (`tests/isolation/`) assert this.
- The handler then runs under `cp_tenant` through `db.WithTenant(ctx, tenant_id, fn)`.
- `/api/sites/{site_id}` resolves the site's `tenant_id` under `cp_system`, then
  applies the same rule.
- A member whose role is not enough for the action gets **403 `owner_required`**.
  Members already know the tenant exists, so 403 reveals nothing.
- Tenants with `cp.tenants.status = 'removed'` answer 404 for every member
  (removal: 5.5).
- A route that checks the tenant status before a write (site create, member
  add) checks it again under the tenant row lock (`LockTenant`), so an
  operator removal, approval or suspension that commits while the request
  waits is what the write sees: removed answers 404, suspended 409
  `tenant_suspended`, and a site's `pending` or `active` status follows the
  locked tenant status.
- Every `cp_tenant` transaction sets `lock_timeout = 5s`. A request that would
  wait longer for a row lock (the reconciler holds the tenant row across its
  Umami calls) answers 503 `unavailable` with `Retry-After` instead of holding
  a database connection.
- `cp_tenant` may update only the `cp.sites` columns its routes write (`name`,
  `origins`, `updated_at`, `status`, `collect_utm`), only `status` and
  `revoked_at` of `cp.otlp_tokens`, and only `role` of `cp.tenant_members`.
  The verification and `stellar_toml` columns of 5.8 and 5.12 are written
  under `cp_system` by the verify route, the stellar.toml fetch after site
  creation and the `verify-domains` task, next to the check that decides
  them, so no tenant handler can mark a domain verified. Every tenant
  transaction carries the session user in `app.user_id` beside
  `app.tenant_id`. Under `cp_tenant` a site's status may only change to
  `removed`, a token only from `active` to `revoked`, audit rows must name
  exactly the session user (`user:<uuid>`) as actor, and member rows may be
  added, changed or removed only when the session user is an owner of the
  tenant (a member may remove itself). Triggers enforce this (migrations
  `00024` and `00028`); operator and system transitions run
  as `cp_system`.
- **Reconcile after a change.** Routes that say "Reconcile runs" ask for a run
  and answer at once. Requests coalesce per tenant: a tenant is queued once,
  and a change that arrives while its run is in flight makes exactly one more
  run follow, so any burst of edits costs at most two runs. At most 4 runs are
  in flight per control plane instance, on a `cp_system` pool of their own
  (4 connections), so session loads never wait behind them. The request pools
  are sized explicitly: `cp_system` 16, `cp_tenant` 16, `cp_operator` 8.

---

## 2. Response conventions

### 2.1 Success

| Situation | Status | Body |
|---|---|---|
| Read | 200 | the object, or a list envelope (2.3) |
| Create | 201 | the created object, with `Location` set to its URL |
| Update | 200 | the full updated object |
| Delete, logout | 204 | none |
| Accepted for asynchronous work | 202 | the object in its new state |

### 2.2 Error envelope

Every 4xx and 5xx response from `/api` (except the two browser redirects, which
redirect instead) has this body:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "origins[1] must be a bare https origin",
    "fields": { "origins[1]": "not_bare_origin" },
    "request_id": "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10"
  }
}
```

- `code`: stable machine value from 2.4. Clients branch on it, never on `message`.
- `message`: English text for people. Its wording can change between releases.
- `fields`: present only for 422; maps a JSON path (`name`, `origins[1]`,
  `enrollment.stellar_ids[0]`) to a field code from 2.5.
- `tenants`: present only for 409 `sole_owner` (4.1.6); a list of
  `{"id", "name"}` of the projects that block the request.
- `request_id`: also sent as response header `X-Request-Id`, and logged.

### 2.3 Lists and pagination

List routes answer:

```json
{ "items": [ ... ], "next_cursor": "eyJ0IjoiMjAyNi0xMS0xMlQwOTowMDowMFoiLCJpIjoiLi4uIn0" }
```

- Query `limit` (1 to 200, default 50) and `cursor` (the opaque `next_cursor` of the
  previous page). `next_cursor` is `null` on the last page.
- Order is fixed per route and documented with it; the cursor is a keyset over that
  order (for example `(created_at, id)`), so pages do not shift when rows are added.
- A cursor from another route or a tampered one: 400 `invalid_cursor`.
- Time-series routes (ingest counts) are not paginated; they bound the time range
  instead.

### 2.4 Error codes

| Status | Code | When |
|---|---|---|
| 400 | `invalid_body` | not JSON, wrong JSON type, unknown field |
| 400 | `invalid_query` | a query parameter is malformed |
| 400 | `invalid_cursor` | see 2.3 |
| 400 | `ceremony_expired` | a passkey answer without matching options of this session, after they expired or were used (5.15) |
| 400 | `step_up_failed` | a passkey answer that does not verify (5.15) |
| 401 | `unauthenticated` | no session, expired session |
| 401 | `step_up_required` | an `/api/operator` route without a recent passkey step-up on the session (5.1, 5.15); the session itself stays valid |
| 401 | `enrollment_closed` | a first passkey from a session whose sign-in is older than the enrollment window (5.15) |
| 403 | `csrf_header_missing` | see 1.2 |
| 403 | `origin_mismatch` | see 1.2 |
| 403 | `owner_required` | the action needs role `owner` |
| 403 | `terms_not_accepted` | the tenant has not accepted the current terms version (4.2.6) |
| 403 | `operator_required` | an `/api/operator` route without operator rights (5.1) |
| 403 | `operator_admin_required` | a roster change by an operator who is not an operator admin (5.16) |
| 403 | `challenge_invalid` | a signed wallet challenge that does not verify: no `sa_stellar` cookie, a challenge of another console, another network, another account, another purpose or another session, or a signature that is not the account's (4.1.7); or a passkey answer that does not verify: no `sa_passkey` cookie, another challenge, origin, relying party, purpose or session, no user verification, or a signature that is not the passkey's key (4.1.9) |
| 403 | `challenge_expired` | the wallet or passkey challenge is older than 5 minutes (4.1.7, 4.1.9) |
| 403 | `challenge_used` | the wallet or passkey challenge was verified before (4.1.7, 4.1.9) |
| 403 | `passkey_unknown` | a passkey answer without a public key names a credential that no live account holds (4.1.9) |
| 403 | `scope_forbidden` | a machine API key scoped to one tenant names another tenant (9.3) |
| 404 | `not_found` | unknown id, malformed id, or not a member |
| 405 | `method_not_allowed` | method not defined for the path |
| 409 | `slug_taken` | `cp.tenants.slug` already exists |
| 409 | `already_member` | invite or add for a user who is a member |
| 409 | `invite_exists` | an open invite for that GitHub account or Stellar address (G or C) exists (`invites_tenant_github_idx`, `invites_tenant_stellar_idx`) |
| 409 | `identity_in_use` | linking a GitHub account, Stellar address or passkey that signs in to another account (4.1.8), or a new passkey whose credential id another account holds (4.1.9) |
| 409 | `identity_already_linked` | linking a GitHub account, Stellar address or passkey to an account that already has one of that kind (4.1.8) |
| 409 | `last_sign_in_method` | unlinking the only sign-in method of an account (4.1.8) |
| 409 | `last_owner` | removing or demoting the last owner |
| 409 | `passkey_required` | a step-up of an account without a passkey (5.15) |
| 409 | `last_passkey` | deleting the last passkey of an account (5.15) |
| 409 | `last_operator_admin` | a roster change or account deletion that would leave no active operator admin (5.16, 4.1.6) |
| 409 | `bootstrap_operator` | suspending, removing or demoting a roster row that the deploy settings ensure (5.16) |
| 409 | `operator_exists` | adding an identity that is on the roster already (5.16) |
| 409 | `sole_owner` | an account deletion while the account is the only owner of a tenant that is not `removed`; the envelope lists those tenants in `tenants` (4.1.6) |
| 409 | `site_removed` | change to a site with `status = 'removed'` |
| 409 | `terms_version_mismatch` | accepted version is not the current one |
| 409 | `invalid_transition` | lifecycle action not allowed from the current status (5.5) |
| 409 | `tenant_suspended` | site create or update while the tenant is `suspended` (5.11); OTLP token create while `suspended` (6.2) |
| 409 | `tenant_not_active` | OTLP token create while the tenant is `pending` or `rejected` (6.2) |
| 409 | `erasure_in_progress` | an erasure request while another one runs for the same site (10.2) |
| 409 | `too_many_keys` | an MCP access key create over 20 active keys of the user (12.8) |
| 410 | `tracker_version_revoked` | snippet for a revoked tracker version |
| 413 | `body_too_large` | body over 64 KiB |
| 415 | `unsupported_media_type` | see section 1 |
| 422 | `validation_failed` | field rules, details in `fields` |
| 429 | `rate_limited` | per-client, per-session or per-route limit; `Retry-After` header set |
| 500 | `internal` | anything unexpected; details only in the log |
| 503 | `unavailable` | database, NATS, Umami or GitHub unreachable, or a tenant request that waited longer than its 5 second lock timeout for the tenant row (a reconcile or an operator action holds it; 1.3); `Retry-After` set |
| 503 | `provisioning_pending` | SSO before the reconciler created the Umami objects; `Retry-After` set |
| 503 | `alert_source_unreachable` | no Alertmanager member answered the operator alerts route (11.2) |
| 503 | `alert_source_not_configured` | the operator alerts route on a control plane without `ALERTMANAGER_URLS` (11.2) |

### 2.5 Field codes (422)

`required`, `too_long`, `too_short`, `too_many`, `invalid_format`, `not_https_url`,
`not_bare_origin`, `origin_outside_domain`, `invalid_domain`, `invalid_strkey`,
`invalid_github_login`, `invalid_role`, `duplicate`, `unknown_tracker_version`,
`unknown_github_login` (a well-formed login that names no GitHub user account,
4.2.5), `invalid_value` (a value outside an enumerated set, such as `mode`) and
`unknown_tenant` (a `tenant_id` that names no tenant or a removed one, 9.2).

---

## 3. Shared objects

### 3.1 Objects

**User** (`cp.users`):

```json
{
  "id": "uuid",
  "github_id": 1234567,
  "github_login": "octocat",
  "stellar_address": null,
  "passkey_address": null,
  "display_name": "octocat",
  "is_operator": false
}
```

An account signs in with GitHub, with a Stellar wallet, with a passkey, or with
several of them linked (4.1.8). Each identity is `null` when the account does
not hold it: `github_id` and `github_login`; `stellar_address`, the G-address of
a wallet (4.1.7); `passkey_address`, the C-address of the soropass account of a
passkey (4.1.9). `display_name` is the GitHub login, or the first and last four
characters of an address with an ellipsis between (`GCB6…WOQN`), the wallet's
before the passkey's, for an account without GitHub.

The Member object carries the same identity fields. The Invite object carries
`github_id`, `github_login`, `stellar_address` and `display_name`; its
`stellar_address` is a G-address or a C-address (4.2.5).

`is_operator` is true when the account's GitHub id, Stellar address or passkey
address names an active row of the operator roster (5.1). The database keeps it
so whenever the account or the roster changes, so a suspension or removal takes
effect at the person's next request. The operator routes of section 5 build on it.

**Tenant** (`cp.tenants` plus the caller's role):

```json
{
  "id": "uuid",
  "slug": "stellar-dex",
  "name": "Stellar DEX",
  "description": "",
  "status": "active",
  "status_reason": null,
  "enrollment_answers": {
    "website": "https://example.org",
    "stellar_usage": "Soroban AMM on mainnet",
    "stellar_ids": ["GA...", "CA..."],
    "scf_round_url": null
  },
  "accepted_terms_version": "2026-11-12-draft.1",
  "provisioning": "ready",
  "role": "owner",
  "created_at": "...",
  "updated_at": "...",
  "status_changed_at": "..."
}
```

- `status`: one of `pending`, `active`, `rejected`, `suspended`, `removed` (column check).
- `provisioning`: `pending` while `cp.tenants.umami_team_id` is null, `ready` after.
  The Umami team id itself is never returned.
- `is_internal`, `created_by`, `approved_by` and `umami_team_id` are not returned to tenants.

**Member** (`cp.tenant_members` joined with `cp.users`):

```json
{ "user_id": "uuid", "github_id": 1234567, "github_login": "octocat", "stellar_address": null,
  "display_name": "octocat", "role": "member", "created_at": "..." }
```

The identity fields follow User.

**Invite** (`cp.invites`):

```json
{ "id": "uuid", "github_id": 7654321, "github_login": "someone", "stellar_address": null,
  "display_name": "someone", "role": "member", "created_by": "uuid", "created_at": "...", "expires_at": "..." }
```

An invite names either a GitHub account or a Stellar account. `github_id` is
the GitHub account the invite is bound to; `github_login` is that account's
login when the invite was made, for display (4.2.5). `stellar_address` is the
G-address of an invite by address; the other two fields are then `null`.

**Site** (`cp.sites`):

```json
{
  "id": "uuid",
  "tenant_id": "uuid",
  "name": "Marketing site",
  "domain": "example.org",
  "origins": ["https://example.org", "https://www.example.org"],
  "quarantine_website_id": "uuid",
  "status": "active",
  "verify_method": null,
  "verified_at": null,
  "collect_utm": false,
  "created_at": "...",
  "updated_at": "..."
}
```

- `status`: `pending`, `active`, `suspended`, `removed` (column check).
- `collect_utm`: whether the gateway keeps the `utm_source`, `utm_medium`,
  `utm_campaign`, `utm_content`, `utm_term` and `ref` query parameters of page
  URLs for this site. `false` on every new site and on every site that existed
  before the field (migration 00027); owners turn it on with 4.3.4. With
  `false` the gateway drops every query parameter. Published to the gateway
  in the site's `registry_sites` entry.
- Site also carries the review, verification and stellar.toml fields listed in
  5.10. `verify_token` is never part of Site; only the verification route of 5.8
  returns it.

### 3.2 Validation rules

| Field | Rule |
|---|---|
| `name` (tenant) | 1 to 100 characters after trimming |
| `slug` | matches the column check `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`; optional on create, derived from `name` when absent |
| `description` | 0 to 1,000 characters |
| `enrollment.website` | absolute `https` URL, at most 2,048 characters |
| `enrollment.stellar_usage` | 1 to 2,000 characters |
| `enrollment.stellar_ids` | 0 to 20 entries, each a valid strkey of type G, M or C (account, muxed account, contract), checked with `github.com/stellar/go-stellar-sdk` strkey |
| `enrollment.scf_round_url` | optional `https` URL, at most 2,048 characters |
| `github_login` | GitHub login syntax: 1 to 39 characters, alphanumeric or single hyphens, not starting or ending with a hyphen; compared case-insensitively |
| `role` | `owner` or `member` (column check) |
| `name` (site) | 1 to 100 characters |
| `domain` | lowercase hostname, IDNA A-label form, at least one dot, no port, no trailing dot, at most 253 characters; immutable after creation |
| `origins` | 1 to 20 entries, unique; each a bare origin (`scheme://host[:port]`, no path, query or fragment, lowercase) whose host is `domain` or a subdomain of it, scheme `https`; exception: `http://localhost[:port]` and `http://127.0.0.1[:port]` for local testing |

Text fields reject control characters other than newline and tab.

### 3.3 Audit

Every mutation writes one `cp.audit_log` row in the same transaction as the change.

- `actor`: `user:<cp.users.id>` for console actions, `system:<task>` for background
  work (`system:reconcile`, `system:umami-rotate`).
- `action`: dotted name from the list below.
- `target`: `tenant:<id>`, `site:<id>`, `user:<id>` or `invite:<id>`.
- `detail`: JSON with the changed fields (old and new values for updates). Never a
  token, cookie, password or secret.

Sign-in, tenants and sites (section 4): `user.sign_in`, `user.sign_out`, `account.delete`, `tenant.create`, `tenant.update`,
`tenant.terms_accept`, `member.add`, `member.role_change`, `member.remove`,
`invite.create`, `invite.revoke`, `invite.resolve`, `site.create`, `site.update`,
`site.delete`, `provision.team_create`, `provision.website_create`,
`provision.website_update`, `provision.website_delete`, `provision.user_create`,
`provision.user_update`, `provision.membership_add`, `provision.membership_remove`,
`provision.registry_put`, `provision.registry_delete`.
`umami-rotate` writes one `umami.password_rotate` row per run (actor
`system:umami-rotate`, `target` empty, detail: day and user count), not one per user.

Operator governance (section 5): `user.operator_sync`, `settings.enrollment_mode`,
`settings.removal_keep_aggregates`, `tenant.approve`, `tenant.reject`,
`tenant.resubmit`, `tenant.suspend`, `tenant.resume`, `tenant.remove`,
`site.review_queue`, `site.approve`, `site.reject`, `purge.step`,
`purge.complete`, `site.verify`, `site.stellar_toml`, `operator.bootstrap`,
`operator.add`, `operator.suspend`, `operator.resume`, `operator.role_change`,
`operator.passkeys_reset`, `operator.remove`, `operator.step_up`,
`operator.passkey_add`, `operator.passkey_delete`. Actors added:
`system:startup` (roster bootstrap and operator sync), `system:verify-domains`, `system:purge`.
Every operator action of section 5 carries `reason` in `detail` when one was given,
and every row about one tenant carries `detail.tenant_id`, which the audit
filter of 5.9 uses.

OTLP tokens (section 6): `otlp_token.create`, `otlp_token.revoke`,
`provision.otlp_registry_put`. Target added: `otlp_token:<id>`.

The API metrics routes (section 7) are read-only and write no audit row.

Erasure (section 10): `erasure.request` (actor `user:<id>`),
`erasure.complete` and `erasure.fail` (actor `system:erasure`). Target added:
`erasure:<id>`. Detail carries `tenant_id`, `site_id`, `kind`, the counts and,
for `session`, `session_id`; never an IP address, user agent or visitor token.

MCP server (section 12): `mcp_credential.create`, `mcp_credential.revoke`,
`mcp_oauth.consent`, `mcp.tool_call`. Actors added: `mcp_credential:<id>`,
`system:mcp-revoke`, `system:mcp-oauth`. Targets added: `mcp_credential:<id>`,
`mcp_client:<id>`.

On-chain witness (section 15): `onchain.add`, `onchain.remove` (actor
`user:<id>`, written under `cp_tenant`), `onchain.challenge` and
`onchain.verify` (actor `user:<id>`, written under `cp_system`, where the
proof columns are written). Target added: `chain_id:<cp.chain_ids.id>`.
Detail: `tenant_id`, `strkey`, `kind` or `method`, and for `onchain.verify`
`result` (`ok` or the verify error code of 15.4). Never a signature.

Exports (section 16): `export.csv` for every export. A tenant export has
actor `user:<id>`, target `tenant:<id>` and detail `tenant_id`, `view`
(`tenant_overview`), `from`, `to` and `rows`. An operator export has target
`operator:<view>` (`web_projects`, `api_projects`, `web_compare`,
`api_compare`) and detail `view`, `query` (the query string it answered,
without a cursor), `rows` and, for compare exports, `tenants`.

Cohorts and threshold rules (section 19): `cohort.propose` (actor
`user:<id>`, written under `cp_tenant`), `cohort.set`, `cohort.confirm`,
`cohort.proposal_dismiss`, `rule.create`, `rule.update`, `rule.delete`
(operator actions, written under `cp_system`). Cohort rows have target
`tenant:<id>` and detail `tenant_id` and the old and new values; rule rows
have target `rule:<id>` and the rule's fields (old and new for an update). A
rule matches export is an `export.csv` row with target `operator:rule_matches`
and detail `rule_id`, `query` and `rows`.

Usage reports (section 20): `report.create` and `report.revoke` (actor
`user:<id>`, written under `cp_tenant`) and `report.view` (actor `public`,
written under `cp_system` for every view through the shared link). Target
added: `report:<id>`. Detail: `tenant_id`, and `from`, `to` and `signer` on
create. A view row holds no address, user agent or any other trait of the
reader.

Operator API keys (section 9): `operator_api_key.create`,
`operator_api_key.revoke`. Target added: `operator_api_key:<id>`. Detail:
`name`, `prefix` and `tenant_id` (null for a key for all tenants), plus
`scope` and `expires_at` on create. Never the key or its hash.

---

## 4. Sign-in, tenants, members, sites, snippet, ingest, SSO

### 4.1 Auth

#### 4.1.1 `GET /api/auth/github/start`

Public. Browser navigation, not `fetch`.

Query: `return_to` (optional): a console path starting with `/` and not with `//`
or `/\`, at most 512 characters. Anything else is replaced with `/`.

Effect: creates `state` (32 random bytes, base64url) and a PKCE verifier
(`oauth2.GenerateVerifier`), sets the `sa_oauth` cookie (1.1), and answers
**302** to `https://github.com/login/oauth/authorize` with `client_id`,
`redirect_uri` (`GITHUB_REDIRECT_URL`), `state`, `code_challenge`,
`code_challenge_method=S256` and `allow_signup=true`. No `scope` parameter is sent:
without a scope GitHub grants read-only access to public information, and the
public `GET /user` answer holds the only two fields the control plane stores
(`id` and `login`). Rate limited inside the control plane (core Caddy has no rate
limiter), per client: one in-memory token bucket per
client and control plane instance, 0.5 per second with a burst of 20, under a
global ceiling per instance of 50 per second with a burst of 200. A request
takes from its client's bucket first, so a client over its limit spends nothing
of the ceiling. When the ceiling is empty, a client whose own bucket was full
(idle for at least 40 seconds) takes from a reserve of 5 per second, burst 20,
instead: a flood keeps its own buckets below full and
cannot reach it with the keys it is using. Over the limits: 429 `rate_limited` with `Retry-After` (JSON
envelope, no redirect, no `sa_oauth` cookie).

The client is the address core Caddy passes in `X-Sa-Client-Addr` (set from the
connection, replacing any value the client sent; `deploy/caddy/app.Caddyfile`),
or the connection's peer when no Caddy is in front (dev). The control plane
keys the bucket by `HMAC-SHA256(k, address)`, where `k` is 32 random bytes made
at process start and held only in memory; IPv4 addresses are used whole, IPv6
addresses by their /48 (the largest assignment one end site normally holds, so
one holder of a /48 gets one bucket, not 65,536). The address is never logged, stored or kept beyond the
request, and Caddy's access log drops all request headers. Buckets that have
refilled are forgotten; at most 100,000 clients are tracked per instance, and
beyond that new clients get 429 until buckets refill.

#### 4.1.2 `GET /api/auth/github/callback`

Public. Browser navigation from GitHub.

Query from GitHub: `code` and `state`, or `error`, `error_description`, `error_uri`
and `state`.

Steps:

1. Read and open `sa_oauth`; delete it in the response whatever happens next.
2. `state` must equal the cookie's `state` (constant-time compare) and the cookie
   must not be expired. Otherwise: failure `state_mismatch`.
3. `error=access_denied`: failure `access_denied`. Any other `error`: failure
   `github_error`.
4. The cookie's `state` must not have reached this step before on this
   control plane instance; a replayed copy of the cookie is failure
   `state_mismatch` (states are remembered until their cookie expires). Then take a token from the exchange buckets: the client's
   (0.5 per second, burst 20, keyed as in 4.1.1) and the instance's ceiling
   (10 per second, burst 30), or, for a client whose bucket was full, the
   reserve (2 per second, burst 10) when the ceiling is empty. Empty: failure `rate_limited`. Then exchange
   `code` with the cookie's verifier (`oauth2.VerifierOption`). Failure:
   `exchange_failed`.
5. `GET https://api.github.com/user` with the access token, headers
   `Accept: application/vnd.github+json` and `X-GitHub-Api-Version: 2022-11-28`.
   Use `id` (int64) and `login` only; the token is then discarded, never stored.
   Failure: `github_unavailable`.
6. Under `cp_system`, in one transaction: upsert `cp.users` (`UpsertUser`: key
   `github_id`, refresh `github_login` and `last_login_at`); resolve open, unexpired
   `cp.invites` whose `github_id` equals the user's GitHub id into
   `cp.tenant_members` and delete those invites (audit `invite.resolve`); audit
   `user.sign_in`. The login is never matched: it can be renamed and taken by
   someone else.
7. Renew the session token, store `user_id`, answer **303** to `return_to`.

Failure: **303** to `/sign-in?error=<code>` with `code` one of `state_mismatch`,
`access_denied`, `github_error`, `rate_limited`, `exchange_failed`,
`github_unavailable`, `internal`. The console shows a message per code. No JSON body.

#### 4.1.3 `POST /api/auth/logout`

Session, CSRF rules apply. Body: none. Destroys the session row and expires the
cookie. Audit `user.sign_out`. **204**. Without a session: also 204 (idempotent).

#### 4.1.4 `GET /api/me`

Session.

```json
{
  "user": { "id": "uuid", "github_id": 1234567, "github_login": "octocat", "stellar_address": null,
            "passkey_address": null, "display_name": "octocat", "is_operator": false },
  "tenants": [
    { "id": "uuid", "slug": "stellar-dex", "name": "Stellar DEX", "status": "active", "role": "owner",
      "accepted_terms_version": "2026-11-12-draft.1", "terms_current": true }
  ],
  "terms": { "current_version": "2026-11-12-draft.1", "status": "draft", "url": "/terms" }
}
```

`tenants` lists every tenant the user is a member of, except `removed`, ordered by
`name`, unpaginated (a user has few). `terms_current` is
`accepted_terms_version = terms.current_version`. 401 without a session.

#### 4.1.5 `GET /api/terms`

Public.

```json
{ "version": "2026-11-12-draft.1", "status": "draft", "markdown": "# Terms ..." }
```

Served from `docs/compliance/terms.md`, embedded in the binary at build time. The
version is the value of the file's front matter `version`.

#### 4.1.6 `DELETE /api/me`

Session, CSRF rules apply. Body: none. Deletes the caller's own console
account. **204**, with the session cookie expired.

Refused with **409 `sole_owner`** while the caller is the only owner of a tenant
whose status is not `removed`; the error envelope lists those tenants in
`tenants` (2.2), ordered by name. Nothing changes then. The caller makes another
member an owner (4.2.5), or asks the operators to remove the tenant (5.5), and
repeats the request. The last active operator admin of the roster gets **409
`last_operator_admin`** and changes nothing; another operator becomes an admin
first (5.16).

Otherwise, in one `cp_system` transaction that first locks the caller's user row,
the rows of every tenant the caller belongs to and those tenants' owner rows:

1. Every membership of the caller is deleted. The membership trigger of
   migration `00030` revokes the caller's MCP credentials for each tenant
   (`mcp_credential.revoke`, reason `member_removed`).
2. Every invite addressed to the caller's GitHub account id, Stellar address or
   passkey address is deleted.
3. The caller's MCP credentials and OAuth codes (section 12) are deleted, with
   their tokens.
4. The roster rows of the caller's GitHub account id and Stellar address (5.1)
   and the caller's passkeys (5.15) are deleted, then every session of the
   caller, in every browser.
5. The `cp.users` row is emptied (migrations `00031` and `00034`): `github_id`,
   `github_login`, `stellar_address`, `passkey_address`, `passkey_credential_id`,
   `passkey_public_key` and `umami_username` become null,
   `is_operator` false, `last_login_at` null, and `deleted_at` is set. The row keeps its `id`, which tenants, sites,
   tokens, keys, erasure requests, removals and audit rows name as their
   creator or actor.
6. The caller's Umami user (named by `cp.users.umami_username`) is deleted, which ends every Umami
   token the caller held. If Umami does not answer, the transaction rolls back
   and the request answers 503 `unavailable`; nothing is deleted.
7. One audit row `account.delete` is written: actor and target `user:<id>`,
   detail with the memberships removed (`tenant_id` and `role` each), the
   counts of invites, MCP credentials and sessions deleted, `was_operator`,
   `roster_removed`, `passkeys_deleted` and `umami_user_deleted`. It names no
   GitHub id, login, Stellar address or passkey.

After the commit a reconcile run is queued for every tenant the caller left.
Audit rows written before the deletion stay unchanged (the log is
append-only); they name the caller by `user:<id>`, and `actor_login` in 5.9 is
`null` for that id from now on. A later sign-in with the same GitHub account
or wallet creates a new user with a new id, no memberships and no invites. The
deleted account's passkey answers 403 `passkey_unknown` from then on; a new
passkey creates a new user. The new user is no operator: the deletion removed
the roster rows. An identity in `OPERATOR_GITHUB_IDS` or
`OPERATOR_STELLAR_ADDRESSES` gets its row back at the next control plane start
(5.1).

401 without a session.

#### 4.1.7 Wallet sign-in: `POST /api/auth/stellar/challenge` and `POST /api/auth/stellar/verify`

Public, CSRF rules apply (1.2). A person proves control of a Stellar account
with a [SEP-10](https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0010.md)
challenge transaction that their wallet signs. SEP-10 is used rather than
SEP-53 message signing because every wallet the console offers signs
transactions, while some cannot sign messages. The console drives the wallet
through the Stellar Wallets Kit's module API (`apps/console/src/lib/stellar-wallet`).

`POST /api/auth/stellar/challenge`, body:

```json
{ "address": "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN", "return_to": "/projects" }
```

- `address` is required and must be an account id (G-address) with a valid
  strkey checksum: 422 `invalid_strkey` otherwise. Muxed (M...) addresses are
  refused. `return_to` is optional and cleaned as in 4.1.1.
- The control plane builds the challenge with the Go Stellar SDK
  (`github.com/stellar/go-stellar-sdk/txnbuild.BuildChallengeTx`): source the
  server account, sequence 0, time bounds of 5 minutes, a first
  `manage_data` operation named `<console host> auth` with the client account
  as source and a 48-byte random nonce (base64) as value, and a
  `web_auth_domain` operation holding the console host (the host of
  `CONSOLE_ORIGIN`). It is signed by the server key for the public network
  (`Public Global Stellar Network ; September 2015`). The server key is derived
  from `OAUTH_STATE_KEY` with HKDF-SHA256 under its own label, so no extra
  setting holds it.
- It sets the `sa_stellar` cookie (1.1) with the nonce, the address, purpose
  `sign_in`, `return_to` and the expiry.
- **200**:

```json
{ "transaction": "AAAAAgAAAAA...", "network_passphrase": "Public Global Stellar Network ; September 2015",
  "expires_at": "2026-11-16T10:05:00Z" }
```

- Rate limited per client and per instance with the same buckets and reserve
  as 4.1.1: 429 `rate_limited` with `Retry-After`.

`POST /api/auth/stellar/verify`, body `{"transaction": "<signed envelope, base64 XDR>"}`.
Steps, each refusal answering 403 with the code named, and the `sa_stellar`
cookie cleared whatever happens:

1. The request takes a token from the exchange buckets of 4.1.2 step 4
   (429 `rate_limited`).
2. The `sa_stellar` cookie opens and names purpose `sign_in`
   (`challenge_invalid`), and is not expired (`challenge_expired`).
3. `txnbuild.ReadChallengeTx` with the server account, the public network
   passphrase and the console host as web auth domain and home domain: the
   envelope is a challenge of this console, inside its time bounds and signed by
   the server (`challenge_invalid`). A challenge that carries a memo or names a
   muxed account is refused.
4. The client account equals the cookie's address and the first operation's
   nonce equals the cookie's nonce (constant-time compare): `challenge_invalid`.
5. `txnbuild.VerifyChallengeTxSigners` with the client account as the only
   signer: the account's own key signed the transaction hash for the public
   network (`challenge_invalid`). A co-signer of a multisig account does not
   prove the account; its master key must sign.
6. Under `cp_system`, in one transaction: the SHA-256 of the nonce is inserted
   into `cp.wallet_challenges_used` with the challenge's expiry, and an
   existing row answers `challenge_used`, so a challenge verifies once across
   every control plane instance (expired rows are deleted in the same step);
   `cp.users` is upserted by `stellar_address` (`SignInStellarUser`); the
   operator flag is set from both identities (5.1); open invites for the
   account's address or GitHub id become memberships (audit `invite.resolve`);
   audit `user.sign_in` with detail `{"method": "stellar", "stellar_address", "is_operator"}`.
7. Reconcile runs for each tenant joined. The session token is renewed and the
   user stored. **200** `{"return_to": "/projects"}`; the console then opens
   that path.

A wallet-only account's `display_name` is its short address (3.1). Its Umami
user is named `u-<account id>`, like every account the reconciler creates after
migration `00034`; the name is stored in `cp.users.umami_username`, which SSO
(4.4.1) logs in with.

#### 4.1.8 Linked sign-in methods

An account holds at most one GitHub identity, one Stellar identity and one
passkey. Linking needs both proofs in one browser session: the session proves
the account, and the GitHub flow, a signed wallet challenge or a passkey answer
proves the identity being added.

- **Link a wallet.** Session, CSRF rules apply.
  `POST /api/me/identities/stellar/challenge` with `{"address": "G..."}` works
  as the sign-in challenge of 4.1.7 with purpose `link` and the session's user
  id sealed into `sa_stellar`; 409 `identity_already_linked` when the account
  has an address. `POST /api/me/identities/stellar` with
  `{"transaction": "..."}` runs steps 1 to 5 of 4.1.7 for purpose `link`,
  requires the challenge's user id to be the session's (`challenge_invalid`),
  marks the challenge used and sets `stellar_address`. **204**. 409
  `identity_in_use` when another live account holds the address.
- **Link GitHub.** `GET /api/auth/github/start?link=1`: session (401 without
  one). The flow of 4.1.1 and 4.1.2 runs with the session's user id sealed into
  `sa_oauth`; the callback requires the session to still hold that user and
  sets `github_id` and `github_login` instead of signing in. It answers **303**
  to `/account?linked=github`, or to `/account?link_error=<code>` with one of
  `identity_in_use`, `identity_already_linked`, `state_mismatch`, the GitHub
  failure codes of 4.1.2 and `internal`.
- **Link a passkey.** Session, CSRF rules apply.
  `POST /api/me/identities/passkey/challenge` with `{}` works as the sign-in
  challenge of 4.1.9 with purpose `link` and the session's user id sealed into
  `sa_passkey`; 409 `identity_already_linked` when the account has a passkey,
  422 when the body holds `return_to`. `POST /api/me/identities/passkey` with
  the answer of 4.1.9 runs its steps 1 to 6 for purpose `link`, requires the
  challenge's user id to be the session's (`challenge_invalid`), marks the
  challenge used and sets the three passkey columns. **204**. 409
  `identity_in_use` when another live account holds the address or the
  credential id. The console creates a new passkey for a link; a passkey that
  another account already holds answers `identity_in_use`.
- A successful link sets the operator flag from every identity, turns open
  invites for the new identity into memberships, and writes `user.identity_link`
  (detail: the kind and the linked identity).
- **Unlink.** `DELETE /api/me/identities/github`,
  `DELETE /api/me/identities/stellar` and `DELETE /api/me/identities/passkey`:
  session, CSRF rules apply. **204**; 404
  `not_found` when the account has no identity of that kind; 409
  `last_sign_in_method` when it is the only one. The operator flag follows the
  remaining identities; audit `user.identity_unlink` (detail: the kind only).
  The account's sessions stay. Signing in with an unlinked GitHub account or
  wallet afterwards creates a new account; an unlinked passkey answers 403
  `passkey_unknown`, because its public key is no longer stored.

#### 4.1.9 Passkey sign-in: `POST /api/auth/passkey/challenge` and `POST /api/auth/passkey/verify`

Public, CSRF rules apply (1.2). A person proves control of a
[soropass](https://docs.soropass.dev/docs) passkey: a WebAuthn credential whose
ES256 (P-256) key is the founding signer of a soropass smart account, a Stellar
contract account. The console runs the ceremonies with `@soropass/core`
(`registerPasskey` and `browserWebAuthnClient`, `apps/console/src/lib/passkey`);
the control plane verifies the WebAuthn assertion with the checks soropass's
`verifyClientDataJSON`, `verifyRpIdHash` and `verifyAssertionSignature` define
and the soropass account contract applies on-chain
(`internal/controlplane/passkey`). Soropass defines no server sign-in protocol
of its own and no SEP-45 flow, so the assertion over a challenge of this
console is the proof.

The relying party is the console: its id is the host of `CONSOLE_ORIGIN`
without a port, and `CONSOLE_ORIGIN` is the only accepted origin.

The identity is the passkey's account address, a C-address: the address the
soropass AccountFactory on the public network
(`CCCNRWMICVEMMUSBI7DL3IKB566QEOOQOLVDOAM5SLFDZ2KGUSRR3JVF`, the default of
`@soropass/core` 0.3.1) deploys the account at, which the control plane
computes as `deriveAccountAddress` does: the contract id of the factory with
salt SHA-256(UTF-8 of the base64url credential id ‖ the 65-byte SEC-1 public
key) for the public network passphrase. The address is bound to both, so no
other key can hold an account at it. The control plane does not deploy the
account and calls no Stellar service.

`POST /api/auth/passkey/challenge`, body `{"return_to": "/projects"}` (optional,
cleaned as in 4.1.1). It sets the `sa_passkey` cookie (1.1) with a 32-byte
random challenge, purpose `sign_in`, `return_to` and the expiry, 5 minutes
ahead. **200**:

```json
{ "challenge": "q2Vy7oZ2yq1mJ8u0Yb4tU0c0qkqQm2m5h3G8n9w6m2E", "rp_id": "app.example.org",
  "timeout_ms": 120000, "expires_at": "2026-11-16T10:05:00Z" }
```

`challenge` is base64url. Rate limited with the buckets and reserve of 4.1.1
(429 `rate_limited`).

The console asks the browser for an assertion with `rpId` = `rp_id`, the
challenge, and user verification `required`:

- **A new passkey.** The console first creates the passkey with soropass's
  `registerPasskey` (ES256 only, a discoverable credential, user verification
  required, attestation `none`), then asks for the assertion with that
  credential only. Registration answers carry no signature, so the control
  plane learns the key from the console and accepts it only together with an
  assertion that the key signed.
- **A known passkey.** The console asks for a discoverable credential; the
  browser offers the passkeys it holds for the console.

`POST /api/auth/passkey/verify`, body, every byte field base64url without
padding:

```json
{ "credential_id": "AQID...", "authenticator_data": "SZYN...", "client_data_json": "eyJ0...",
  "signature": "MEUC...", "public_key": "BB8U..." }
```

`public_key` is the 65-byte SEC-1 key of a new passkey and is omitted for a
known one. Each field is required except `public_key`: 422 `required`,
`invalid_format` (not base64url, or a credential id over 1023 bytes),
`too_long` (authenticator data over 1024 bytes, client data over 4096, a
signature over 80), `invalid_value` (a key that is not an uncompressed P-256
point). Then, each refusal answering 403 with the code named and the
`sa_passkey` cookie cleared whatever happens:

1. The request takes a token from the exchange buckets of 4.1.2 step 4
   (429 `rate_limited`).
2. The `sa_passkey` cookie opens and names purpose `sign_in`
   (`challenge_invalid`), and is not expired (`challenge_expired`).
3. Without `public_key`, the key is the one stored for `credential_id` on a
   live account; none: `passkey_unknown`.
4. `client_data_json` is JSON with `type` `webauthn.get`, `challenge` equal to
   the cookie's (constant-time compare), `origin` equal to `CONSOLE_ORIGIN` and
   no `crossOrigin` true (`challenge_invalid`).
5. `authenticator_data` holds at least 37 bytes, its first 32 are SHA-256 of
   the relying party id, and its flags carry user present and user verified
   (`challenge_invalid`). The soropass account contract requires user
   verification at signing time, so the console requires it too. The
   signature counter is not checked: synced passkeys report 0.
6. The DER `signature` is ECDSA P-256 over
   SHA-256(`authenticator_data` ‖ SHA-256(`client_data_json`)) by the key
   (`challenge_invalid`). The control plane derives the account address.
7. Under `cp_system`, in one transaction: SHA-256 of `passkey:` and the
   challenge is inserted into `cp.wallet_challenges_used` with the expiry, an
   existing row answering `challenge_used`; `cp.users` is upserted by
   `passkey_address` with the credential id and the key (`SignInPasskeyUser`;
   a credential id that another live account holds answers 409
   `identity_in_use`); the operator flag is set from every identity (5.1);
   open invites for the account's addresses or GitHub id become memberships
   (audit `invite.resolve`); audit `user.sign_in` with detail
   `{"method": "passkey", "passkey_address", "is_operator"}`.
8. Reconcile runs for each tenant joined. The session token is renewed and the
   user stored. **200** `{"return_to": "/projects"}`.

A passkey-only account's `display_name` is its short address (3.1). Its Umami
user is named `u-<account id>`, as in 4.1.7.

`cp.users` stores `passkey_address`, `passkey_credential_id` and
`passkey_public_key`, all three or none (migration `00037`), with live-row
unique indexes on the address and the credential id. `cp_tenant` reads
`passkey_address` only, for member lists.

A passkey belongs to its relying party: a passkey created on another site,
including another soropass application, is not offered by the browser on the
console and cannot sign in.

**Use for later passkey checks.** `passkey.RelyingParty.Verify` with a fresh
challenge and the account's stored `passkey_public_key` is the one WebAuthn
verification of the control plane. A later step that asks a signed-in person
to confirm with their passkey (an operator step-up, for example) issues its
own challenge with a purpose of its own in `sa_passkey` and calls it, so the
console keeps one WebAuthn approach and one stored credential per account.

### 4.2 Tenants

#### 4.2.1 `POST /api/tenants`

Session. Enrollment.

```json
{
  "name": "Stellar DEX",
  "slug": "stellar-dex",
  "description": "",
  "enrollment": {
    "website": "https://example.org",
    "stellar_usage": "Soroban AMM on mainnet",
    "stellar_ids": ["GA..."],
    "scf_round_url": "https://communityfund.stellar.org/..."
  },
  "accepted_terms_version": "2026-11-12-draft.1",
  "cohort": { "program": "Build Award", "round": "SCF #38", "category": "DeFi", "award_date": "2026-10-01" }
}
```

- `cohort` is optional: the project's proposal of its SCF cohort (19.1), at
  least one field, written as the proposal in the same transaction and named
  in the `tenant.create` audit row as `cohort_proposal`.
- `slug` and `description` optional, `enrollment.stellar_ids` and
  `enrollment.scf_round_url` optional; the rest required (3.2).
- `accepted_terms_version` must equal the current version: else 409
  `terms_version_mismatch`.
- Under `cp_system`, one transaction: insert `cp.tenants` (`enrollment_answers` =
  the `enrollment` object, `accepted_terms_version`, `created_by`, `status` from
  `cp.settings` `enrollment_mode`: `open` gives `active`, `approval` (5.7) gives
  `pending`), insert the creator into `cp.tenant_members` with role
  `owner`, audit `tenant.create`. Then trigger `Reconcile(tenant_id)`
  asynchronously.
- **201** Tenant (3.1), `provisioning: "pending"` until the reconciler finishes.
- Errors: 409 `slug_taken`, 409 `terms_version_mismatch`, 422, 429 (at most 10
  tenants created per user per day).

#### 4.2.2 `GET /api/tenants`

Session. List envelope of Tenant for the caller's memberships, excluding
`removed`, ordered by `(created_at, id)`.

#### 4.2.3 `GET /api/tenants/{tenant_id}`

Session, member. **200** Tenant.

#### 4.2.4 `PATCH /api/tenants/{tenant_id}`

Session, owner. Body: any of `name`, `description`. These are the only tenant
columns `cp_tenant` may update (`00003_cp_security.sql`). Editing
`enrollment_answers` is the resubmission route of 5.7, after a rejection.
**200** Tenant. Audit `tenant.update`.

#### 4.2.5 Members and invites

`GET /api/tenants/{tenant_id}/members`: session, member.

```json
{ "members": [Member, ...], "invites": [Invite, ...] }
```

Members ordered by `github_login`, or by the wallet address, then the passkey
address, for a member without GitHub, invites by `created_at`. Expired invites are
not listed. Not paginated (bounded by the limits below).

`POST /api/tenants/{tenant_id}/members`: session, owner.

```json
{ "github_login": "someone", "role": "member" }
```

or, for a person who signs in with a Stellar wallet or a passkey,

```json
{ "stellar_address": "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN", "role": "member" }
```

Exactly one of `github_login` and `stellar_address` is given (422
`invalid_value` on `stellar_address` for both, `required` on `github_login` for
neither). An address is an account id (G-address) of a wallet or the account
address (C-address) of a passkey (4.1.9), with a valid checksum (422
`invalid_strkey`). An address names the account that signs in with it: an
existing account is added at once (`member.add`), anyone else gets an invite
bound to the address (`invite.create`, detail includes `stellar_address`) that
their first wallet sign-in with a G-address (4.1.7 step 6) or passkey sign-in
with a C-address (4.1.9 step 7) resolves. The rest
of this section describes the GitHub case; limits and errors apply to both.

- The login is first resolved to the GitHub account that holds it now:
  `GET https://api.github.com/users/{login}` with the OAuth app's client id and
  secret as basic auth (GitHub's public-data rate limit for an OAuth app,
  instead of the 60 per hour of an anonymous caller), headers as in 4.1.2
  step 5. Everything below uses that account's numeric `id`; the login is kept
  for display only. A login renamed later, and
  then taken by someone else, never moves a membership or an invite.
- If a `cp.users` row with that GitHub id exists, the user is added to
  `cp.tenant_members` at once and the row's `github_login` is refreshed to
  GitHub's current login: **201** `{"kind": "member", "member": Member}`, audit
  `member.add`.
- Otherwise an invite bound to that GitHub id is created with
  `expires_at = now() + 14 days`: **201** `{"kind": "invite", "invite": Invite}`,
  audit `invite.create` (detail includes `github_id`). It is resolved when that
  account next signs in, under whatever login it has then (4.1.2 step 6).
- A login GitHub does not know, or one that names an organization (which
  cannot sign in), is 422 `unknown_github_login` on `github_login`. GitHub
  unreachable or answering anything else: 503 `unavailable`.
- Invites made before migration `00025` carried no GitHub id; that migration
  revoked them (one `invite.revoke` audit row each, actor `system:migration`),
  and owners send them again.
- Errors: 409 `already_member`, 409 `invite_exists`, 422, 422
  `unknown_github_login`, 503 `unavailable`, and 422 `too_many` on field
  `github_login` when the tenant would exceed 50 members plus open invites.
- Reconcile runs after the change (Umami membership).

`PATCH /api/tenants/{tenant_id}/members/{user_id}`: session, owner. Body
`{"role": "owner" | "member"}`. **200** Member. 409 `last_owner` when demoting the
last owner. Audit `member.role_change`.

`DELETE /api/tenants/{tenant_id}/members/{user_id}`: session, owner, or any member
for their own `user_id` (leaving). **204**. 409 `last_owner`. Access ends on the
next request because membership is read per request. Reconcile removes the Umami
team membership. Audit `member.remove`.

`DELETE /api/tenants/{tenant_id}/invites/{invite_id}`: session, owner. **204**.
Audit `invite.revoke`.

#### 4.2.6 `POST /api/tenants/{tenant_id}/terms`

Session, owner. Body `{"version": "..."}`. Must equal the current version, else 409
`terms_version_mismatch`. Sets `cp.tenants.accepted_terms_version`. **200** Tenant.
Audit `tenant.terms_accept`.

While a tenant's accepted version is not current, every mutating tenant route
except this one and member removal answers 403 `terms_not_accepted`. Reads keep
working. A tenant accepts a new terms version through this route.

### 4.3 Sites

#### 4.3.1 `POST /api/tenants/{tenant_id}/sites`

Session, member.

```json
{ "name": "Marketing site", "domain": "example.org", "origins": ["https://example.org"] }
```

- The control plane makes `id` and `quarantine_website_id` (UUIDv7 each) and
  `verify_token` (32 random bytes, base64url, used by the domain check of 5.8).
- `status` is `active` when the tenant is `active`, else `pending`, with the
  tenant status read under the tenant row lock (1.3): a tenant removed while
  the request waited answers 404, one suspended meanwhile 409
  `tenant_suspended`.
- Under `cp_tenant`: insert `cp.sites` (`CreateSite`), audit `site.create`.
  Reconcile then creates the two Umami websites and publishes `registry_sites`.
- **201** Site.
- Errors: 422, 422 `too_many` on field `name` above 50 non-removed sites per tenant.

#### 4.3.2 `GET /api/tenants/{tenant_id}/sites`

Session, member. List envelope of Site, ordered by `(created_at, id)`. Query
`include_removed=true` adds sites with `status = 'removed'`.

#### 4.3.3 `GET /api/tenants/{tenant_id}/sites/{site_id}`

Session, member. **200** Site.

#### 4.3.4 `PATCH /api/tenants/{tenant_id}/sites/{site_id}`

Session, member. Body: any of `name`, `origins` (whole list replaced),
`collect_utm` (boolean; any other JSON type answers 400). `domain` is
immutable (422 `invalid_format` on `domain`). Changing `collect_utm` needs the
owner role (403 `owner_required` for a member): it changes the site's
privacy position. A member may send the current value unchanged. **200** Site.
409 `site_removed`.
Audit `site.update`, with the old and new value of each changed field; a body
that changes nothing writes no audit row and starts no reconcile. Reconcile
republishes the registry entry, so the gateway applies new origins and the
`collect_utm` setting within its watcher delay.

#### 4.3.5 `DELETE /api/tenants/{tenant_id}/sites/{site_id}`

Session, owner. Sets `status = 'removed'` (the row stays: `cp.erasure_requests`
references it, and the review rule of 5.6 reads the domains of removed tenants' sites). **202** Site with
`status: "removed"`. Reconcile deletes both Umami websites (and their data) and the
`registry_sites` key. Audit `site.delete`. Repeating the call: 202, no change.

#### 4.3.6 `GET /api/sites/{site_id}/snippet`

Session, member of the site's tenant.

Query: `version` (optional; default the newest non-revoked version of
`packages/tracker/releases/manifest.json`).

```json
{
  "site_id": "uuid",
  "version": "1.0.0",
  "src": "https://in.<BASE_DOMAIN>/v/1.0.0/sdk.js",
  "integrity": "sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j",
  "tag": "<script defer src=\"https://in.<BASE_DOMAIN>/v/1.0.0/sdk.js\" integrity=\"sha384-...\" crossorigin=\"anonymous\" data-website-id=\"uuid\"></script>",
  "npm": null,
  "versions": [ { "version": "1.0.0", "released": "2026-11-12", "bytes": 6734, "gzip_bytes": 3273 } ]
}
```

- `tag` is built by `internal/controlplane/snippet.go` and is byte-identical to
  `snippet({siteId, version, origin})` in `packages/tracker/src/snippet.ts` with
  `origin = INGEST_ORIGIN` (attribute order and quoting are part of that contract).
- `integrity` equals the manifest's `integrity` for the version.
- `versions`: every non-revoked manifest entry, newest first.
- `npm`: `null`. The console offers the script tag and hides the npm tab while
  `npm` is `null`. The control plane refuses to start when `TRACKER_NPM_PACKAGE`
  is set (4.5), so it never answers with an install line for a package name it
  has not been built for.
- Errors: 404 `not_found` (site), 422 `unknown_tracker_version` on `version`, 410
  `tracker_version_revoked`, 409 `site_removed`.

#### 4.3.7 `GET /api/sites/{site_id}/ingest`

Session, member. Hourly outcome counts from `cp.web_ingest_hourly`.

Query: `from`, `to` (RFC 3339, whole hours, `from < to`; default the last 24 full
hours), `bucket` (`hour` default, or `day` in UTC). Range at most 31 days for
`hour`, 400 days for `day`; else 400 `invalid_query`.

```json
{
  "site_id": "uuid",
  "from": "2026-11-11T09:00:00Z",
  "to": "2026-11-12T09:00:00Z",
  "bucket": "hour",
  "series": [
    { "at": "2026-11-11T09:00:00Z", "counts": { "accepted": 120, "quarantined": 3, "rejected_origin": 2 } }
  ],
  "totals": { "accepted": 2810, "quarantined": 41, "bot": 9, "dlq": 0, "rejected_origin": 17 }
}
```

- Outcomes are the values stored in `cp.web_ingest_hourly.outcome`: `accepted`,
  `quarantined`, `bot`, `dlq` and `rejected_<reason>` (`internal/events`
  `outcomeRe`). Unknown future outcomes are passed through; the console shows them
  under "other rejections".
- Buckets with no rows are omitted from `series`; `counts` holds only non-zero
  outcomes.
- Rows are written by the `jobs` task `edgestats` and lag by up to one fold
  interval; the console says "updated every few minutes".

### 4.4 SSO to Umami

#### 4.4.1 `GET /api/sso/umami`

Session, member of the site's tenant. Browser navigation from a console link.

Query: `site` (site id, required), `view` (`primary` default, or `quarantine`).

- Requires `Sec-Fetch-Site` `same-origin` or `none` when the header is present;
  otherwise failure `cross_site`.
- The site must not be `removed` or `pending` (in review, 5.6: the reconciler
  creates no Umami website for it); the tenant's Umami user and team membership
  must exist (reconciler done). Otherwise failure `provisioning_pending`.
- Logs in to Umami on the private network (`POST /api/auth/login`) as the
  member's `cp.users.umami_username` (`u-<account id>` for accounts provisioned
  after migration `00034`, `gh-<github_id>` for earlier ones; failure
  `provisioning_pending` while it is unset) with that user's derived password
  for the current UTC day. It never
  sets a password.
- **302** to `https://web.<BASE_DOMAIN>/sso?token=<token>&url=/websites/<id>` where
  `<id>` is the site id for `primary` and `quarantine_website_id` for `quarantine`.
  Response headers: `Cache-Control: no-store`, `Referrer-Policy: no-referrer`.
- The token is never logged. Caddy on `app.` cuts the query from its access log;
  the response `Location` header is removed from logs as well.
- Failure: **303** to `/sso-error?code=<code>&site=<site_id>`, code one of
  `not_found`, `cross_site`, `provisioning_pending`, `umami_unavailable`, `internal`.
- Rate limit: 30 per user per minute; over it the failure code is `rate_limited`.
- **Removed members.** Reconcile removes the Umami team membership of a removed
  member at once, but it does not change that user's password, so an Umami token
  the person already holds still passes `/api/auth/verify` until the next daily
  `umami-rotate`. Every request for the tenant's websites with that token is refused
  with 401 from the moment the membership is gone, so no data is reachable; only the
  token itself stays syntactically valid for at most about a day.

Attribution: every console page that links into Umami carries
`<a href="https://db-ip.com">IP Geolocation by DB-IP</a>`, and every HTML page
Caddy serves on `web.<BASE_DOMAIN>` carries it too. The DB-IP Lite licence asks
for a link back to DB-IP on every page that displays or uses results from the
database, and an Umami page is a separate page on its own host.

### 4.5 Configuration keys (control plane)

Loaded by `internal/config`; a missing required key stops the process and names
the key.

| Key | Required | Meaning |
|---|---|---|
| `CONTROLPLANE_ADDR` | yes | API listener, for example `0.0.0.0:8085`; Caddy reaches it as `CONTROLPLANE_UPSTREAM` |
| `CONTROLPLANE_OPS_ADDR` | yes | `/healthz`, `/readyz`, `/metrics` |
| `CONSOLE_ORIGIN` | yes | exact `Origin` accepted on state changes (1.2) |
| `INGEST_ORIGIN` | yes | `https://in.<BASE_DOMAIN>` for snippets |
| `WEB_ORIGIN` | yes | `https://web.<BASE_DOMAIN>` for SSO redirects |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | yes | the GitHub OAuth App of the environment |
| `GITHUB_REDIRECT_URL` | yes | `<CONSOLE_ORIGIN>/api/auth/github/callback`, checked at startup |
| `OPERATOR_GITHUB_IDS` | yes | comma separated numeric GitHub user ids, at least one; each is ensured as an active operator admin of the roster at every start (5.1) |
| `OPERATOR_STELLAR_ADDRESSES` | no | comma separated Stellar addresses, checksum checked, ensured like `OPERATOR_GITHUB_IDS`: account ids (G-addresses) of operators who sign in with a wallet, and passkey account addresses (C-addresses, 4.1.9) of operators who sign in with a passkey; empty when unset |
| `OPERATOR_STEP_UP_MAX_AGE` | no | how long a passkey step-up opens the operator area (5.15); default `12h`, `5m` to `24h` |
| `OPERATOR_ENROLL_WINDOW` | no | how long a first passkey may be registered without a step-up (5.15); default `24h`, `10m` to `168h` |
| `OAUTH_STATE_KEY` | yes | 32 bytes, base64; seals `sa_oauth`, `sa_stellar` and `sa_passkey`, and derives the key that signs wallet challenges (4.1.7) |
| `REPORT_SIGNING_SEED` | yes | the Stellar secret seed (`S...`) that signs usage reports (20.2) |
| `REPORT_SIGNING_KEY` | yes | the published account id (`G...`) of that seed; the process refuses to start when the seed does not derive it |
| `REPORT_SIGNING_RETIRED_KEYS` | no | comma separated account ids of earlier report keys, newest first; their reports still verify (20.2) |
| `UMAMI_URL`, `UMAMI_ADMIN_PASSWORD` | yes | Umami's address on the private network and its admin password |
| `UMAMI_USER_PASSWORD_KEY` | yes | 32 bytes, base64; derives daily Umami passwords; shared with `jobs` |
| `TRACKER_RELEASE_DIR` | yes | holds `manifest.json` and `revoked.json` |
| `TRACKER_NPM_PACKAGE` | no | leave unset: the control plane refuses to start when it is set (4.3.6) |
| `NATS_URL`, `NATS_REPLICAS` | yes | NATS address and stream replica count |
| `NATS_USER`, `NATS_PASSWORD` | when the server requires auth | NATS credentials |
| database keys | yes | as `internal/db` `PoolConfig` for `cp_system`, `cp_tenant` and `cp_operator` |

In builds with tag `oauthfixture` only: `OAUTH_FIXTURE_URL` replaces the three
GitHub URLs. Production builds have no such key.

## 5. Operator governance

Operators review projects, switch enrollment modes, suspend and remove
projects, and see domain checks. The tables and columns this section adds come
from migrations `00010_tenant_removals.sql` (`cp.tenant_removals`) and
`00011_sites_status_reason.sql` (`cp.sites.status_reason`, `cp.operator_sites`).

### 5.1 Operator identity

- The operator roster is the table `cp.operators` (migration `00036_operator_roster.sql`). Each row
  names one identity, a GitHub account id or a Stellar address (a wallet's G-address or a passkey's
  C-address, 4.1.9), with a role (`operator` or
  `operator_admin`) and a status (`active` or `suspended`). Operator admins manage it in the console
  (5.16).
- `cp.users.is_operator` is derived from the roster in the database: it is true for a live account
  that holds an identity of an active row. The trigger `users_operator_flag` computes it whenever a
  `cp.users` row is written, and `operators_sync_flags` recomputes it for the matching accounts in
  the transaction that changes a roster row. Nothing else writes it. A flag that turns false ends
  the person's operator MCP credentials in the same transaction (12.8).
- At every start the control plane bootstraps the roster from `OPERATOR_GITHUB_IDS` and
  `OPERATOR_STELLAR_ADDRESSES`: each listed identity becomes an active `operator_admin` row
  (audit `operator.bootstrap`, actor `system:startup`, target `operator:<id>`, written only when the
  row was inserted or changed), each listed identity whose account holds no passkey gets a
  first-passkey window (5.15), and every live account's flag is recomputed (audit
  `user.operator_sync`, detail `{"is_operator": {"old", "new"}}`, for each account that changed).
  The settings only add: an identity taken out of them keeps its row until an operator admin
  removes it. The control plane refuses to start when the roster has no active operator admin
  afterwards.
- Every route under `/api/operator/` is **session** and **operator**: the session user must have
  `cp.users.is_operator = true`, read per request. Otherwise **403 `operator_required`**, for every
  method and for unknown ids alike. A suspended or removed operator is refused at their next
  request.
- Every route under `/api/operator/` except those of 5.15 also needs a **passkey step-up** on the
  session younger than `OPERATOR_STEP_UP_MAX_AGE`. Otherwise **401 `step_up_required`**. A request
  authenticated by an MCP credential (12) has no console session and needs no step-up; the machine
  API (9) is not under `/api/operator/`.
- CSRF rules (1.2) apply unchanged. The tenant-scoping rules of 1.3 do not apply
  to operator routes: an operator sees every tenant except `is_internal` ones,
  which answer 404 on operator routes.
- Database roles: reads of `cp.tenants`, `cp.operator_sites`,
  `cp.web_ingest_hourly`, `cp.settings` and `cp.tenant_removals` run as
  `cp_operator`; actions, setting changes, the member list of 5.4 and the audit
  list of 5.9 run as `cp_system`.
- Operators are not made members of tenants and get no Umami access.

### 5.2 Operator objects

**Reason** (request field of 5.5 and 5.6): `reason`, 1 to 1,000 characters after
trimming, text rules of 3.2. Required where marked, otherwise optional (an
approval or resume note). Stored in `cp.tenants.status_reason` (or
`cp.sites.status_reason`) and in the audit `detail.reason`. The tenant sees it.

**OperatorTenantSummary** (list item, 5.3):

```json
{
  "id": "uuid",
  "slug": "stellar-dex",
  "name": "Stellar DEX",
  "status": "pending",
  "status_reason": null,
  "website": "https://example.org",
  "created_at": "...",
  "status_changed_at": "...",
  "site_count": 2,
  "sites_in_review": 0,
  "sites_verified": 1,
  "sites_with_stellar_toml": 1
}
```

- `website` is `enrollment_answers.website`.
- `site_count` counts sites with `status <> 'removed'`. `sites_in_review` counts
  `pending` sites of an `active` tenant (5.6). `sites_verified` uses the rule of
  5.8. `sites_with_stellar_toml` counts sites whose `stellar_toml.status` is
  `found`.

**OperatorSite** (`cp.operator_sites`; never `verify_token`):

```json
{
  "id": "uuid",
  "name": "Marketing site",
  "domain": "example.org",
  "origins": ["https://example.org"],
  "status": "active",
  "status_reason": null,
  "in_review": false,
  "domain_previously_removed": false,
  "verification": "verified",
  "verify_method": "dns",
  "verified_at": "...",
  "last_verify_at": "...",
  "last_verify_error": null,
  "stellar_toml": { "...": "StellarToml of 5.12" },
  "stellar_toml_checked_at": "...",
  "created_at": "...",
  "updated_at": "..."
}
```

- `in_review`: `status = 'pending'` and the tenant is `active`.
- `domain_previously_removed`: the domain equals, is a subdomain of, or is a
  parent of the domain of a site whose tenant is `removed` (5.6).
- `verification`: `verified`, `unverified` or `unchecked` (5.8).

### 5.3 `GET /api/operator/tenants`

Operator. List envelope (2.3) of OperatorTenantSummary.

Query:

| Parameter | Values | Default |
|---|---|---|
| `status` | comma-separated subset of `pending`, `active`, `rejected`, `suspended`, `removed` | all statuses |
| `review` | `true`: only tenants that need an operator, that is `status = 'pending'` or `sites_in_review > 0` | absent |
| `q` | 1 to 100 characters; case-insensitive substring of `name`, `slug`, `enrollment_answers.website` or any site `domain` | absent |
| `sort` | `created_at`, `status_changed_at`, `name` | `created_at` |
| `order` | `asc`, `desc` | `desc` |
| `limit`, `cursor` | as 2.3 | |

- Keyset over `(<sort column>, id)` in the requested order; the cursor encodes
  the sort and order, and a cursor used with another sort or order is 400
  `invalid_cursor`. `name` sorts by `lower(name)`.
- Tenants with `is_internal = true` are never listed.
- One query serves every sort: the keyset runs over `(sort key, id)`, where the
  sort key of a timestamp is its UTC ISO form with microseconds and the key of
  `name` is `lower(name)`.
- 400 `invalid_query` for an unknown status, sort or order, or `q` out of range.

### 5.4 `GET /api/operator/tenants/{tenant_id}`

Operator. **200**:

```json
{
  "tenant": {
    "id": "uuid", "slug": "...", "name": "...", "description": "...",
    "status": "pending", "status_reason": null,
    "enrollment_answers": { "website": "...", "stellar_usage": "...", "stellar_ids": ["G..."], "scf_round_url": null },
    "accepted_terms_version": "...",
    "provisioning": "pending",
    "created_by": { "id": "uuid", "github_id": 1, "github_login": "octocat" },
    "approved_by": null,
    "created_at": "...", "updated_at": "...", "status_changed_at": "..."
  },
  "stellar_ids": [ { "id": "G...", "type": "account", "valid": true } ],
  "members": [ { "github_id": 1, "github_login": "octocat", "role": "owner", "created_at": "..." } ],
  "sites": [ "OperatorSite", "..." ],
  "ingest": {
    "last_24h": { "accepted": 2810, "quarantined": 41, "rejected_origin": 17 },
    "last_30d": { "accepted": 90210 }
  },
  "removal": null,
  "recent_audit": [ "AuditEntry of 5.9, newest 20 with detail.tenant_id = this tenant" ],
  "allowed_actions": ["approve", "reject", "remove"]
}
```

- `stellar_ids`: the enrollment ids re-checked with `strkey` (`type` is
  `account`, `muxed_account` or `contract`).
- `members` excludes nothing; operators see logins to contact a project. Read
  under `cp_system`.
- `created_by` and `approved_by` are `null` when that account was deleted
  (4.1.6).
- `ingest` sums `cp.web_ingest_hourly` over all the tenant's sites for the last 24
  full hours and 30 full days (UTC), non-zero outcomes only.
- `removal`: `null`, or for a removed tenant
  `{"requested_at", "requested_by", "keep_aggregates", "completed_at", "steps": {"registry": {"done_at": "..."}, ...}}`
  from `cp.tenant_removals`.
- `allowed_actions`: the actions of 5.5 valid from the current status, so the
  console shows only those buttons.
- 404 `not_found` for unknown, malformed or internal ids.

### 5.5 Tenant lifecycle actions

All are `POST`, operator, body `{"reason": "..."}` (5.2), run under `cp_system`
in one transaction with the tenant row locked (`for update`), write one audit
row (actor `user:<operator id>`, target `tenant:<id>`, detail
`{"tenant_id", "status": {"old", "new"}, "reason"}`), set `status_changed_at =
now()` and `updated_at = now()`, and then trigger `Reconcile(tenant_id)`
asynchronously. **200** OperatorTenantSummary in the new state.

| Route | From | To | `reason` | Also |
|---|---|---|---|---|
| `.../tenants/{tenant_id}/approve` | `pending` | `active` | optional | `approved_by` = operator; every `pending` site of the tenant becomes `active`; `status_reason` cleared |
| `.../tenants/{tenant_id}/reject` | `pending` | `rejected` | required | nothing was provisioned (5.7) |
| `.../tenants/{tenant_id}/suspend` | `active` | `suspended` | required | registry entries switch to `suspended`; Umami dashboards stay readable |
| `.../tenants/{tenant_id}/resume` | `suspended` | `active` | optional | registry entries switch back to `active`; `status_reason` cleared |
| `.../tenants/{tenant_id}/remove` | any except `removed` | `removed` | required | see below |

(`...` is `/api/operator`.) Any other combination: **409 `invalid_transition`**
with the current status in `message`. Repeating an action on a tenant already in
the target state is also 409, so a double click never writes two audit rows.

**Remove**, in the same transaction:

1. `cp.tenants.status = 'removed'`, `status_reason` = the reason.
2. Every site of the tenant: `status = 'removed'`, `updated_at = now()`. Domains
   stay on the rows, which drives the review rule of 5.6.
3. Open `cp.invites` of the tenant are deleted, so none resolves later.
4. Insert `cp.tenant_removals (tenant_id, requested_at, requested_by, reason,
   keep_aggregates, completed_at = null, steps = '{}')`, `keep_aggregates` copied
   from `cp.settings.removal_keep_aggregates` at this moment.

Then the purge runs in the reconciler, store by store, in this order: `registry`
(site keys), `otlp_tokens` (revokes every token row and flips every
OTLP key to `revoked`, 6.4), `umami` (both
websites of every site with their data, the team, then Umami users whose console
user has no membership in any non-removed tenant), `web_ingest`
(`cp.web_ingest_hourly` rows; skipped when `keep_aggregates`), `api`
(`api.purge_tenant`; minute rows and writer state always, hourly, daily and
warning rows unless `keep_aggregates`),
`agg` (8.7; skipped when `keep_aggregates`). Each finished store writes
`steps.<name>` and an audit row `purge.step` (actor `system:purge`); the last one
sets `completed_at` and writes `purge.complete`. A failed store is retried on the
next reconcile. The tenant row, its member rows and all audit rows remain.
Console sessions of former members are kept too: removal ends their access to
the tenant (every tenant route answers 404), not their sign-in, and the member
rows let `/api/me` list the project under `removed_tenants` (5.10).
After removal every tenant route answers 404 to former members (1.3).

### 5.6 Sites in review

A site enters review when, at creation, its `domain` equals, is a subdomain of,
or is a parent of the domain of any site whose tenant has `status = 'removed'`.
Such a site is created with `status = 'pending'` even in an `active` tenant and in
`open` mode, and the creation writes `site.review_queue` (detail: `domain`,
`matched_domain`, `matched_tenant_id`). `cp_tenant` cannot see other tenants'
sites, so the match runs under `cp_system` just before the insert, with the same
predicate as `cp.operator_sites.domain_previously_removed`, which then shows the
same result to operators. A site deleted by its own owner (tenant not removed)
never triggers review. The reconciler treats a `pending` site like a pending
tenant: nothing in Umami, nothing in the registry.

`POST /api/operator/sites/{site_id}/approve`: operator, body `{"reason"?}`. From
`pending` in an `active` tenant to `active`. Audit `site.approve`. Reconcile.
**200** OperatorSite.

`POST /api/operator/sites/{site_id}/reject`: operator, body `{"reason"}`
(required). From `pending` in an `active` tenant to `removed`, with
`status_reason`. Audit `site.reject`. **200** OperatorSite.

Any other state: 409 `invalid_transition`. Sites of a `pending` tenant are
approved with the tenant (5.5), not one by one.

### 5.7 Enrollment mode and pending tenants

`GET /api/operator/settings/enrollment-mode`: operator. **200**
`{"mode": "open"}` from `cp.settings` key `enrollment_mode`.

`PUT /api/operator/settings/enrollment-mode`: operator. Body
`{"mode": "open" | "approval"}`; anything else is 422 `invalid_value` on `mode`.
Writes the setting under `cp_system`, audit `settings.enrollment_mode` (target
`setting:enrollment_mode`, detail `{"mode": {"old", "new"}}`). **200**
`{"mode": "..."}`. Setting the current value: 200, no audit row.

- The mode is read at each enrollment (4.2.1), so a switch takes effect for the
  next enrollment without a restart and never changes existing tenants.
- `pending` and `rejected` tenants: the reconciler creates nothing in Umami and
  publishes nothing to `registry_sites`, so the gateway answers
  `rejected_unknown_site` for their sites. SSO answers `provisioning_pending`.
  Owners can still add, edit and delete sites and manage members while
  `pending` or `rejected`.

**Resubmission.** `PUT /api/tenants/{tenant_id}/enrollment`: session, owner,
tenant `rejected` (else 409 `invalid_transition`). Body: `enrollment` (required,
rules of 3.2), `name` and `description` (optional). Runs under `cp_system` after
the check that the caller is an owner (cp_tenant has no right on `status` or `enrollment_answers`, on
purpose), with `where status = 'rejected'` in the update. Sets
`enrollment_answers`, the optional fields, `status = 'pending'`,
`status_reason = null`, `status_changed_at = now()`. Audit `tenant.resubmit`
(detail: changed fields old and new). Terms rule of 4.2.6 applies. **200**
Tenant. The tenant appears in the review queue (5.3 `review=true`).

### 5.8 Site domain verification

Proves control of `cp.sites.domain`. The result is evidence for operators; it
never gates ingestion.

`GET /api/sites/{site_id}/verification`: session, member of the site's tenant.

```json
{
  "site_id": "uuid",
  "domain": "example.org",
  "verify_token": "43 characters base64url",
  "dns": {
    "name": "_veristar.example.org",
    "type": "TXT",
    "value": "veristar-verify=<verify_token>"
  },
  "file": {
    "url": "https://example.org/.well-known/veristar.txt",
    "body": "<verify_token>"
  },
  "verification": "unverified",
  "verify_method": null,
  "verified_at": null,
  "last_verify_at": "...",
  "last_verify_error": "dns_no_record"
}
```

This is the only route that returns `verify_token`.

`POST /api/sites/{site_id}/verify`: session, member. Body `{}` or
`{"method": "dns" | "file"}` (absent: DNS first, then the file; first success
wins). Runs the check synchronously (at most about 15 seconds: 5 for DNS, 10 for
the file), writes the result under `cp_tenant`, audit `site.verify` (detail:
method tried, result code). **200** the object above. 409 `site_removed`. 429
`rate_limited` above 6 checks per site per hour (`Retry-After` set). The limit
counts the site's `site.verify` audit rows of the last hour, so it holds across
all control plane instances without shared memory.

- DNS check: a TXT record at `dns.name` whose value (strings of one record
  joined) equals `dns.value` exactly.
- File check: `GET file.url` through the request-forgery guard
  (`internal/netguard`), status 200, body at most 1,024 bytes, equal to the
  token after trimming trailing whitespace.
- On success: `verify_method`, `verified_at = now()`, `last_verify_at = now()`,
  `last_verify_error = null`. On failure: `last_verify_at = now()`,
  `last_verify_error = <code>`; `verified_at` and `verify_method` keep the last
  success.
- `verification`: `verified` when `verified_at` is not null and
  `last_verify_error` is null; `unchecked` when `last_verify_at` is null;
  `unverified` otherwise.
- `last_verify_error` codes: `dns_no_record`, `dns_mismatch`, `dns_error`,
  `file_not_found` (any status other than 200), `file_mismatch`,
  `file_too_large`, `refused_address`, `redirect_refused`, `tls_error`,
  `timeout`, `fetch_error`. When both methods were tried, the file method's code
  is stored and the DNS code is in the audit detail.
- The `jobs` task `verify-domains` (actor `system:verify-domains`) re-runs both
  checks for every non-removed site of a non-removed tenant whose
  `last_verify_at` is older than 24 hours, plus the stellar.toml fetch (5.12). Its
  `site.verify` audit row is written only when the result changes (verification
  value or error code), so the daily run does not add one row per site per day;
  `POST .../verify` always writes one.

The request-forgery guard is the only way the control plane fetches a tenant's
domain. It connects over HTTPS to port 443 only, refuses host names such as
`localhost` or names ending in `.localhost`, `.internal`, `.local` or
`.home.arpa`, refuses loopback, private, link-local, multicast and other
non-public addresses (`refused_address`), follows at most 3 redirects and only
to the same host over HTTPS (`redirect_refused`), and gives a request 10
seconds.

### 5.9 `GET /api/operator/audit`

Operator. List envelope of AuditEntry, ordered by `(at desc, id desc)`, under
`cp_system`.

```json
{ "id": 1234, "at": "...", "actor": "user:uuid", "actor_login": "octocat",
  "action": "tenant.suspend", "target": "tenant:uuid",
  "detail": { "tenant_id": "uuid", "status": { "old": "active", "new": "suspended" }, "reason": "..." } }
```

- `id` is the `cp.audit_log` identity (a number). `actor_login` is the
  `github_login` for `user:` actors, else `null`. It is also `null` for the
  actor of a deleted account (4.1.6).
- Query filters, all optional and combined with AND: `tenant_id` (rows whose
  `target` is `tenant:<id>` or whose `detail.tenant_id` is `<id>`), `target`
  (exact), `action` (exact, or a prefix ending in `.`, such as `tenant.`),
  `actor` (exact), `from` and `to` (RFC 3339, `from < to`, at most 400 days
  apart), `limit`, `cursor`.
- 400 `invalid_query` for a malformed value.

### 5.10 Tenant-visible status and reason

- Tenant (3.1) already carries `status`, `status_reason` and
  `status_changed_at`. The console shows a banner on every project page for
  `pending` ("waiting for review"), `rejected` (reason plus the resubmit form of
  5.7), `suspended` (reason; data stays readable, new events are refused). No
  banner for `active`.
- `GET /api/me` (4.1.4) gains `removed_tenants`: projects the user is a member of
  whose status is `removed`, each
  `{"id", "name", "status_reason", "status_changed_at"}`, newest first, at most
  20. Every other route still answers 404 for them (1.3). The console shows each
  once as a notice with the reason.
- Site (3.1) gains `status_reason`, `in_review`, `verification`,
  `last_verify_at`, `last_verify_error`, `stellar_toml` and
  `stellar_toml_checked_at`. `verify_token` stays out of Site (5.8 only).

### 5.11 Effects of status on tenant routes

| Tenant status | Tenant routes | Sites | Umami and registry |
|---|---|---|---|
| `pending` | all (reads and owner actions) | create allowed, created `pending` | nothing created, nothing published |
| `rejected` | all, plus resubmit | create allowed, created `pending` | nothing |
| `active` | all | created `active`, or `pending` in review (5.6) | provisioned, registry `active` |
| `suspended` | reads, members, terms; site create and update answer 409 `tenant_suspended`; site delete allowed | unchanged | kept, registry `suspended` |
| `removed` | 404 (1.3) | all `removed` | purged (5.5) |

### 5.12 stellar.toml signal

Fetched for the site's `domain` at site creation (asynchronously, after the
response) and by `verify-domains` daily, from
`https://<domain>/.well-known/stellar.toml` through the request-forgery guard,
body at most 102,400 bytes. Stored in `cp.sites.stellar_toml` with
`stellar_toml_checked_at`. It is evidence for operators and decides nothing.

**StellarToml**:

```json
{
  "status": "found",
  "error": null,
  "http_status": 200,
  "bytes": 2048,
  "version": "2.7.0",
  "network_passphrase": "Public Global Stellar Network ; September 2015",
  "network": "public",
  "accounts_count": 3,
  "accounts_valid": 3,
  "accounts": ["G...", "G...", "G..."],
  "signing_key_present": true,
  "signing_key_valid": true,
  "org_url": "https://example.org",
  "org_url_same_domain": true,
  "currencies": [
    { "code": "USDX", "issuer": "G...", "contract": null, "valid": true },
    { "code": "TKN", "issuer": null, "contract": "C...", "valid": true }
  ],
  "currencies_total": 2
}
```

- `status`: `found` (parsed), `not_found` (any status other than 200),
  `invalid` (not TOML, or wrong type for a read field), `too_large`, `refused`
  (address or redirect refused by the guard), `error` (timeout, TLS, network).
  Every field except `status`, `error` and `http_status` is `null` (or 0, false,
  empty list) unless `status` is `found`.
- `error`: the guard or parser code for non-`found` statuses
  (`refused_address`, `redirect_refused`, `tls_error`, `timeout`, `fetch_error`,
  `toml_syntax`, `toml_type`), else `null`.
- `network`: `public`, `testnet`, `futurenet` (the SDK `network` constants),
  `other` for any other passphrase, `null` when absent.
- `accounts`: the valid G accounts of `ACCOUNTS`, at most 50 in file order. The
  two-way link of 15.5 reads them. A site checked before this field existed
  carries no `accounts` until its next daily check, and reads as an empty list.
- `org_url` is `[DOCUMENTATION].ORG_URL`; `org_url_same_domain` compares its host
  with `domain` (SEP-1: the file "must be hosted on the same domain").
- `currencies`: at most 50 entries kept, `currencies_total` is the full count.
  `valid` is true when exactly one of `issuer` (valid G strkey) or `contract`
  (valid C strkey) is set. `code` is cut to 12 characters. No other text from the
  file is stored, so tenant-controlled prose never reaches the console.
- Tenants see the same object on their Site; operators on OperatorSite.
- Audit `site.stellar_toml` only when `status` or `network` changes from the
  previous value, so the daily run does not write one row per site per day.

### 5.13 `removal_keep_aggregates`

`GET /api/operator/settings/removal-keep-aggregates`: operator. **200**
`{"keep": false}` from `cp.settings` key `removal_keep_aggregates` (seeded
`false` by `00004_seed.sql`).

`PUT /api/operator/settings/removal-keep-aggregates`: operator. Body
`{"keep": true | false}`. Audit `settings.removal_keep_aggregates` (detail old
and new). **200** `{"keep": ...}`.

- The value is copied into `cp.tenant_removals.keep_aggregates` when a tenant is
  removed (5.5), so changing it later never changes a removal in progress.
- `true` keeps `cp.web_ingest_hourly` rows, `api.metrics_hourly`,
  `api.metrics_daily` and `api.ingest_warnings` rows and `agg` rows of the
  removed tenant. Umami websites, their raw data and `api` minute rows are
  always deleted.

### 5.14 Configuration keys

Required: `PLATFORM_DB_CP_OPERATOR_PASSWORD`, because operator reads run as
`cp_operator` (5.1). `OPERATOR_GITHUB_IDS` and `OPERATOR_STELLAR_ADDRESSES`
(4.5) bootstrap the roster at start (5.1). `OPERATOR_STEP_UP_MAX_AGE` and
`OPERATOR_ENROLL_WINDOW` (4.5) set the step-up of 5.15. The `jobs` task `verify-domains` needs no key of its own;
the request-forgery guard has no configuration key that could allow private
addresses (tests use a `_test.go` hook only).

### 5.15 Passkey step-up

The operator area needs a second factor on top of the sign-in: a WebAuthn credential (a passkey or
a security key) registered to the console account. Proving one records a step-up on the session.
The routes below sit behind the operator check of 5.1 and not behind the step-up, because they are
how a session gets one. Ceremony state lives in the session and is used once; options and
responses carry binary fields as base64url strings, as the WebAuthn JSON forms do. The relying
party id is the host of `CONSOLE_ORIGIN` and the only accepted origin is `CONSOLE_ORIGIN`. User
verification is preferred, attestation is not requested.

| Route | Needs | Answer |
|---|---|---|
| `GET /api/operator/step-up` | operator | `StepUpStatus` |
| `POST /api/operator/step-up/options` | operator | `{"publicKey": PublicKeyCredentialRequestOptions}`; **409 `passkey_required`** without a passkey |
| `POST /api/operator/step-up` | the options of this session | `StepUpStatus`; body `{"credential": PublicKeyCredential}` |
| `GET /api/operator/passkeys` | step-up | `{"items": [Passkey]}` |
| `POST /api/operator/passkeys/options` | step-up, or an open enrollment window | `{"publicKey": PublicKeyCredentialCreationOptions}` |
| `POST /api/operator/passkeys` | step-up, or an open enrollment window | **201** `Passkey`; body `{"name": 1 to 64 characters, "credential": PublicKeyCredential}` |
| `DELETE /api/operator/passkeys/{passkey_id}` | step-up | **204**; **409 `last_passkey`** for the account's last passkey |

`StepUpStatus` is `{stepped_up, stepped_up_at, expires_at, max_age_seconds, passkeys, enrollment,
enrollment_until}`. `POST /api/operator/step-up` answers without `passkeys`, `enrollment` and
`enrollment_until`. `enrollment` is `open` (a first passkey may be registered without a step-up
until `enrollment_until`), `sign_in_required` (a window is open, but this session's sign-in is
older than it) or `closed`. `Passkey` is `{id, name, created_at, last_used_at, backup_eligible}`;
`backup_eligible` is the authenticator's flag, true for a passkey that a platform syncs between
devices.

- **Step-up.** A verified assertion renews the session token, stores the time and the account id on
  the session (`operator_step_up_at`, `operator_step_up_user`) and writes `operator.step_up`. The
  step-up counts for `OPERATOR_STEP_UP_MAX_AGE` (default `12h`, `5m` to `24h`). An assertion whose
  signature counter did not increase is refused as a possible clone.
- **First passkey.** A roster row gets an enrollment window of `OPERATOR_ENROLL_WINDOW` (default
  `24h`, `10m` to `168h`) when an admin adds it, when an admin resets its passkeys, and at start for
  a bootstrap row whose account holds no passkey. Inside the window, an account without a passkey
  may register one without a step-up, from a session whose sign-in happened after the window
  opened; an older session gets **401 `enrollment_closed`**. The first registration closes the
  window and records a step-up. Later registrations need a step-up.
- **Errors.** **400 `ceremony_expired`**: no options of this session to verify against, they expired
  (5 minutes) or they were used. **400 `step_up_failed`**: the response does not verify (wrong key,
  wrong origin, wrong challenge, replayed counter).
- **Audit.** `operator.step_up`, `operator.passkey_add` (detail `passkey_id`, `name`, `enrollment`,
  `backup_eligible`) and `operator.passkey_delete`, actor and target the account.
- **Other step-up checks.** Approving an MCP authorization request with the operator scope (12.8)
  also needs a fresh step-up, and answers 401 `step_up_required` otherwise. The credential it issues
  then works without one.

### 5.16 Operator roster

Every operator reads the roster; operator admins change it. Every change takes a `reason` of 1 to
500 characters, runs in one `cp_system` transaction and writes an audit row with the reason, actor
`user:<id>`, target `operator:<id>`.

| Route | Needs | Body | Answer |
|---|---|---|---|
| `GET /api/operator/roster` | step-up | | `{"items": [RosterEntry]}` |
| `POST /api/operator/roster` | step-up, admin | `{github_login or stellar_address, role, reason}` | **201** `RosterEntry`; audit `operator.add` |
| `POST /api/operator/roster/{entry_id}/suspend` | step-up, admin | `{reason}` | `RosterEntry`; `operator.suspend` |
| `POST /api/operator/roster/{entry_id}/resume` | step-up, admin | `{reason}` | `RosterEntry`; `operator.resume` |
| `POST /api/operator/roster/{entry_id}/role` | step-up, admin | `{role, reason}` | `RosterEntry`; `operator.role_change` |
| `POST /api/operator/roster/{entry_id}/reset-passkeys` | step-up, admin | `{reason}` | `RosterEntry`; `operator.passkeys_reset` |
| `POST /api/operator/roster/{entry_id}/remove` | step-up, admin | `{reason}` | **204**; `operator.remove` |

`RosterEntry` is `{id, kind ("github" or "stellar"), github_id, github_login, stellar_address, role,
status, status_reason, bootstrap, enrollment_until, created_at, updated_at, account}`. `bootstrap`
is true for an identity in `OPERATOR_GITHUB_IDS` or `OPERATOR_STELLAR_ADDRESSES`. `account` is the
live console account holding the identity, `{user_id, display_name, last_login_at, passkeys}`, or
null before its first sign-in. A GitHub login is resolved to its account id through GitHub's users
API at add time, as for members (4.2.5); the row is bound to the id.

- **403 `operator_admin_required`**: a change by an operator without an active `operator_admin`
  row.
- **409 `last_operator_admin`**: the change would leave no active operator admin. The constraint
  trigger `operators_last_admin` refuses such a change whatever statement makes it.
- **409 `bootstrap_operator`**: a suspension, removal or demotion of a bootstrap row; the next start
  would undo it. Take the identity out of the settings first.
- **409 `operator_exists`**: the identity is on the roster already. **409 `invalid_transition`**: a
  suspension of a suspended row, a resumption of an active one, or a role the row holds already.
- **Effects.** A suspension and a removal turn the flag off at once (5.1), which also revokes the
  person's operator MCP credentials, and end every console session of the account. A removal also
  deletes the account's passkeys unless another active row keeps it an operator. A passkey reset
  deletes every passkey of the account, ends its sessions and opens a new enrollment window.
- **Account deletion (4.1.6)** removes the roster rows of the account's identities and its passkeys.
  The last active operator admin gets **409 `last_operator_admin`** instead.

---

## 6. OTLP tokens

Tenants create bearer tokens for the OTLP collector (`otlp.<BASE_DOMAIN>`). The
collector reads the NATS key-value bucket `registry_otlp` and accepts a token only while its
entry has `status = "active"`.

### 6.1 Token object

`cp.otlp_tokens` without the hash:

```json
{
  "id": "uuid",
  "tenant_id": "uuid",
  "name": "Production API",
  "environment": "production",
  "prefix": "sa_otlp_Ab3x",
  "status": "active",
  "created_by": "uuid",
  "created_at": "...",
  "revoked_at": null,
  "last_seen_at": null
}
```

- Format: `sa_otlp_` followed by 40 characters drawn uniformly from
  `0-9A-Za-z` with `crypto/rand`.
- Storage: `hash` is the SHA-256 of the whole token (32 bytes); `prefix` is the
  first 12 characters (`sa_otlp_` plus 4 random ones), for display only. The
  token itself is stored nowhere: not in the database, the audit log, the
  registry or a log line.
- `status`: `active` or `revoked` (column check). `last_seen_at` is stamped by
  the metrics writer.

### 6.2 `POST /api/tenants/{tenant_id}/otlp-tokens`

Session, member, current terms (4.2.6).

```json
{ "name": "Production API", "environment": "production" }
```

- `name`: 1 to 100 characters (text rules of 3.2). `environment`: optional,
  empty or `^[a-z0-9]([a-z0-9._-]{0,31})?$`, else 422 `invalid_format`.
- The tenant must be `active`: 409 `tenant_suspended` while `suspended`, 409
  `tenant_not_active` while `pending` or `rejected`. The status is read again
  under the tenant row lock in the insert transaction, so a concurrent
  suspension waits for the insert and then reconciles the new token.
- At most 50 `active` tokens per tenant: 422 `too_many` on `name`.
- Under `cp_tenant`: insert, audit `otlp_token.create` (detail: `tenant_id`,
  `name`, `environment`, `prefix`), then write the `registry_otlp` entry
  `{tenant_id, token_id, status: "active"}` under the hex SHA-256 before the
  commit. A registry failure rolls the insert back: **503 `unavailable`**.
- **201** Token object plus `"token": "sa_otlp_..."`, with `Location` set to the
  token's URL. This answer is the only place the token ever appears.

### 6.3 `GET /api/tenants/{tenant_id}/otlp-tokens` and `GET .../otlp-tokens/{token_id}`

Session, member, any non-removed status. The list is the envelope of 2.3 over
Token objects, ordered by `(created_at, id)`. Query `include_revoked=true` adds
revoked tokens. A token of another tenant is 404 `not_found` (1.3).

### 6.4 `DELETE /api/tenants/{tenant_id}/otlp-tokens/{token_id}`

Session, member. Allowed in every non-removed status and without current terms,
because taking a credential out of use must never be blocked.

- Under `cp_tenant` with the row locked: `status = 'revoked'`,
  `revoked_at = now()`, audit `otlp_token.revoke` (detail `tenant_id`,
  `status` old and new), then the `registry_otlp` entry is set to
  `status: "revoked"` before the commit. A registry failure rolls back: 503
  `unavailable`, so a revocation never looks done while the key still says
  `active`.
- **204**. Repeating the call: 204, no new audit row, the registry entry is
  written again (which heals a lost write).

### 6.5 Registry state and reconcile

The entry of every token is a function of its row and its tenant's status:

| Token | Tenant | `registry_otlp` status |
|---|---|---|
| `revoked` | any | `revoked` |
| `active` | `active` | `active` |
| `active` | `pending`, `rejected`, `suspended` | the tenant status |
| any | `removed` | `revoked` (the purge revokes every row) |

- Keys are never deleted, so an inactive token is refused as such, not as
  unknown.
- The reconciler (`provision`, after every lifecycle action and every 10
  minutes in `jobs`) writes each entry that differs, first in its run and
  before any Umami call, so suspension and resume reach the collector even
  while Umami is unreachable. Each write is audited as
  `provision.otlp_registry_put` (actor `system:reconcile`, target
  `otlp_token:<id>`, detail `tenant_id`, `status`).
- Removal (5.5): the `otlp_tokens` purge step runs right after `registry`. It
  sets every active row to `revoked` and flips every key to `revoked`; its step
  detail lists `tokens_revoked` and `keys_revoked`. Revocation by removal is
  final.
- NATS users: `controlplane` and `jobs` may write `registry_otlp` (jobs runs the
  reconcile task); `collector` only watches it.

### 6.6 Effects of tenant status (extends 5.11)

| Tenant status | Token create | Token list, get, revoke |
|---|---|---|
| `pending`, `rejected` | 409 `tenant_not_active` | allowed |
| `active` | allowed | allowed |
| `suspended` | 409 `tenant_suspended` | allowed |
| `removed` | 404 | 404 |

---

## 7. API metrics

A tenant's own API traffic, read from the metrics writer's tables `api.metrics_minute`,
`api.metrics_hourly`, `api.metrics_daily` and `api.ingest_warnings`
(`docs/reference/api-storage.md`). Handlers live in
`internal/controlplane/apimetrics`, queries in
`db/queries/tenant/apimetrics.sql`. Every query runs under `cp_tenant` through
`db.WithTenant`, so row-level security limits it to the tenant in the path even
if a handler passed the wrong one.

All six routes are `GET`, session, member, any non-removed tenant status
(`pending`, `rejected`, `active`, `suspended`); current terms are not required.
A non-member or a removed tenant gets 404 `not_found` (1.3). No new error code:
every malformed or unknown query parameter is 400 `invalid_query`, a bad cursor
400 `invalid_cursor`.

| Route | Section |
|---|---|
| `GET /api/tenants/{tenant_id}/api-metrics/overview` | 7.5 |
| `GET /api/tenants/{tenant_id}/api-metrics/timeseries` | 7.6 |
| `GET /api/tenants/{tenant_id}/api-metrics/routes` | 7.7 |
| `GET /api/tenants/{tenant_id}/api-metrics/status` | 7.8 |
| `GET /api/tenants/{tenant_id}/api-metrics/services` | 7.9 |
| `GET /api/tenants/{tenant_id}/api-metrics/warnings` | 7.10 |

### 7.1 Query parameters

| Parameter | Routes | Rule |
|---|---|---|
| `range` | all | `1h`, `24h`, `7d`, `30d`, `90d`, `12mo`, `24mo` or `custom`; default `24h` |
| `from`, `to` | all, only with `range=custom` (both required then, refused otherwise) | RFC 3339 with `Z` or a numeric offset; `from` not before `2000-01-01T00:00:00Z`, `from < to`, `to` at most 24 hours after server time, `to - from` at most 800 days |
| `service` | overview, timeseries, routes, status | exact `service.name` |
| `route` | overview, timeseries, routes, status | exact normalized `http.route` (for example `/v1/accounts/{id}`, or `{other}` for routes above the daily cap) |
| `method` | overview, timeseries, routes, status | exact `http.request.method` (`GET`, `_OTHER`, ...) |
| `sort`, `order`, `limit`, `cursor` | routes | 7.7 |

- A parameter not listed for the route, or given twice, is 400 `invalid_query`.
- Filter values are compared for equality and always sent to PostgreSQL as bound
  parameters, never spliced into SQL, so quotes, `%`, `_` and SQL keywords are
  literal characters that simply match nothing unless a stored value has them.
- A filter present with an empty value (`service=`) matches the empty value,
  which is how a point without that attribute is stored. An absent parameter
  means no filter.
- Filter values must be valid UTF-8 without control characters (U+0000 to
  U+001F, U+007F) and at most the column limit in bytes: `service` 128,
  `method` 16, `route` 1,024. Anything else is 400 `invalid_query`.

### 7.2 Range and resolution

Server time `now` is UTC. Each request resolves to a half-open range
`[from, to)` and one resolution: `minute` (rows of `api.metrics_minute`),
`hour` (`api.metrics_hourly`) or `day` (`api.metrics_daily`). Every bucket of
an answer has that one resolution; the only other table read is
`api.metrics_minute` for hours still pending a rollup (freshness, below).

| Preset | Resolution | `to` | `from` |
|---|---|---|---|
| `1h` | minute | start of the current minute + 1 minute | `to` − 60 minutes |
| `24h` | hour | start of the current hour + 1 hour | `to` − 24 hours |
| `7d` | hour | same | `to` − 168 hours |
| `30d` | hour | same | `to` − 720 hours |
| `90d` | day | start of the current UTC day + 1 day | `to` − 90 days |
| `12mo` | day | same | `to` − 12 calendar months |
| `24mo` | day | same | `to` − 24 calendar months |

`custom`: the resolution follows `to − from` as given: `minute` up to and
including 6 hours, `hour` up to and including 31 days, `day` beyond. Then `from`
is moved back to the start of its unit and `to` forward to the next unit
boundary (unless already on one).

Retention step, applied after either of the above, in this order:

1. Resolution `minute` and `from` before the minute horizon (start of the
   current UTC day minus `API_MINUTE_RETENTION_DAYS` days, the first day whose
   minute partition the retention job keeps): the resolution becomes `hour`,
   `from` and `to` are aligned to hours, `resolution_note` is
   `older_than_minute_retention`.
2. Resolution `hour` and `from` before the hourly horizon (first day of the
   current UTC month minus `API_HOURLY_RETENTION_MONTHS` months): the resolution
   becomes `day`, aligned to days, `resolution_note` is
   `older_than_hourly_retention`.

This is a documented rule, not a fallback: the answer always says which
resolution it has.

**Freshness.** The `api-rollup` job recomputes hourly and daily rows every 5
minutes from the hours the writer marked in `api.rollup_pending`. Until it has
run, an `hour` or `day` answer computes each still-marked hour from
`api.metrics_minute` instead of its stale hourly row, and a day containing one
from its unmarked hourly rows plus its marked hours from minutes. Rolled-up and
minute-derived rows are never both counted for the same hour: the rollup
removes a mark in the transaction that replaces that hour's rows, and each
query reads marks and rows in one statement. So hourly and daily answers show
traffic as soon as the writer has stored it, and their numbers do not change
when the rollup runs. (A pending hour before the minute horizon cannot occur:
the retention job keeps a minute partition until its marks are rolled up.)

**Range object**, in every answer:

```json
{
  "preset": "24h",
  "from": "2026-11-11T10:00:00Z",
  "to": "2026-11-12T10:00:00Z",
  "resolution": "hour",
  "bucket_seconds": 3600,
  "resolution_note": null
}
```

- `preset`: the requested preset, `custom`, or `previous` for the comparison
  range of 7.5.
- `bucket_seconds`: 60, 3600 or 86400 (UTC days).
- `resolution_note`: `null`, `older_than_minute_retention` or
  `older_than_hourly_retention`.

### 7.3 Source selection

Metric rows (`source = metric`, from `http.server.request.duration`) count every
request. Span rows (`source = span`, derived from SERVER spans) undercount when
the tenant samples traces. For each service, over the whole resolved range at
the resolved resolution (and only the `service` filter applied, never `route`
or `method`, so filtering a route cannot switch a service's source), an answer
uses:

- `metric` rows when at least one metric row of that service exists in the
  range,
- otherwise `span` rows.

The two are never added together. A service that moved from spans to metrics
inside the range uses metric rows for the whole range and its span rows are
left out, as for any service that has both. The selection is
`internal/controlplane/apimetrics/source.go`; sections 8 and 9 apply the same rule.

**Source object**, one per service with any row in the range, ordered by
`service`, in every answer except warnings:

```json
{ "service": "payments-api", "source": "span", "span_data_ignored": false }
```

- `source`: `metric` or `span`. The console shows a "from traces" badge for
  `span` with one line: "Derived from sampled traces; counts are lower than the
  real request count when traces are sampled."
- `span_data_ignored`: true when the service has span rows in the range besides
  its metric rows, which is the normal case for a service that exports both
  metrics and traces. Only the metric rows are used. The console shows
  "metric data used; span-derived data ignored for this service". It does not
  imply the service switched sources; a service that moved from spans to
  metrics during the range shows the same flag.

The selection and the aggregate are two statements in one read committed
transaction; a row committed between them can be counted, but a service's
source cannot change between them in a way that mixes sources, because the
aggregate is restricted to the selected source of each service.

### 7.4 Numbers

Every row carries a traffic class (`traffic_class`, migration `00035`): `app`, or one of the
infrastructure classes `preflight`, `health` and `probe` that the collector assigns
([collector reference](collector.md), "Traffic classes"). Every number below except
`infrastructure_requests` covers rows of class `app` only, so health checks, readiness probes and
CORS preflights never inflate the volume, the error rates or the latency percentiles.

- `requests`: `sum(count)` of the selected `app` rows.
- `infrastructure_requests`: `sum(count)` of the selected rows of the other classes.
- `errors_4xx`: requests with `status_code` 400 to 499; `errors_5xx`: 500 to
  599. `status_code` 0 means the point had no status attribute; it counts in
  `requests` and in class `unknown`, never as an error.
- `rate_4xx`, `rate_5xx`: errors divided by `requests`, a fraction between 0 and
  1; `null` when `requests` is 0.
- `p50`, `p95`, `p99`: `api.histogram_quantile(q, api.sum_buckets(counts))`
  over exactly the rows the number describes: bucket arrays are
  summed first, then interpolated linearly inside the bucket. Percentiles are
  never averaged, neither across rows, buckets, services nor time. Value: a JSON
  number in seconds; the string `">10 s"` when the percentile falls in the open
  bucket above 10 s (never a number); `null` when there are no requests.
- `lossy`: true when any selected row was re-bucketed inexactly from a
  non-canonical histogram layout, so percentiles are approximate. The
  console shows "approximate" next to such percentiles.

**Summary object:**

```json
{
  "requests": 1200, "errors_4xx": 30, "errors_5xx": 2,
  "rate_4xx": 0.025, "rate_5xx": 0.0016666666666666668,
  "p50": 0.042, "p95": 0.31, "p99": ">10 s", "lossy": false,
  "infrastructure_requests": 8640
}
```

### 7.5 `GET .../api-metrics/overview`

Parameters: range and filters. The summary of the range and of the preceding
range of equal length: `previous.from = from − (to − from)`,
`previous.to = from`, resolved with the retention step of 7.2 on its own
(`preset: "previous"`), with its own source selection.

```json
{
  "current":  { "range": { ... }, "sources": [ ... ], "summary": { ... } },
  "previous": { "range": { ... }, "sources": [ ... ], "summary": { ... } },
  "traffic_classes": { "rule_set_version": 1, "applied_from": "2026-11-16T09:00:00Z" }
}
```

`traffic_classes.applied_from` is when the deployment's first traffic class rule set took effect
(the time migration `00035` ran). Rows written before it hold every request as `app`, as they were
counted then.

### 7.6 `GET .../api-metrics/timeseries`

Parameters: range and filters. One point per bucket from `from` (inclusive) to
`to` (exclusive), in time order, including buckets without data (zeros,
`null` rates and percentiles), so a chart needs no gap filling. At most 801
points (a 24-month range has 730 or 731).

```json
{
  "range": { ... },
  "sources": [ ... ],
  "points": [
    { "at": "2026-11-12T09:00:00Z", "requests": 52, "errors_4xx": 1, "errors_5xx": 0,
      "rate_4xx": 0.019230769230769232, "rate_5xx": 0, "p50": 0.031, "p95": 0.2, "p99": 0.48, "lossy": false,
      "infrastructure_requests": 360 }
  ]
}
```

Each point's percentiles come from the bucket arrays of that time bucket only.

### 7.7 `GET .../api-metrics/routes`

Parameters: range, filters, `sort`, `order`, `limit`, `cursor`. One item per
service, method and route with at least one selected row. A route that only infrastructure
requests reached, such as `GET /healthz`, is listed with `requests` 0 and its
`infrastructure_requests`.

- `sort`: `requests` (default), `errors_4xx`, `errors_5xx`, `rate_4xx`,
  `rate_5xx`, `p50`, `p95`, `p99`, `route` (orders by service, method, route).
- `order`: `asc` or `desc`; default `asc` for `route`, `desc` otherwise.
- Ties are broken by service, method and route ascending. `null` values sort
  last in both orders. `">10 s"` sorts above every number.
- `limit` 1 to 200, default 50. `cursor` is the opaque `next_cursor` of the
  previous page. Pages are offsets into the sorted list: the cursor is bound to
  the route, `sort`, `order`, `range`, `from`, `to` and the filters, and is 400
  `invalid_cursor` with any other combination. Because the numbers keep
  changing as data arrives, an item can move between pages; `total` is the item
  count at the time of the request.

```json
{
  "range": { ... },
  "sources": [ ... ],
  "total": 134,
  "items": [
    { "service": "payments-api", "method": "GET", "route": "/v1/accounts/{id}", "source": "metric",
      "requests": 930, "errors_4xx": 12, "errors_5xx": 0, "rate_4xx": 0.012903225806451613, "rate_5xx": 0,
      "p50": 0.04, "p95": 0.3, "p99": 0.9, "lossy": false, "infrastructure_requests": 0 }
  ],
  "next_cursor": null
}
```

### 7.8 `GET .../api-metrics/status`

Parameters: range and filters.

```json
{
  "range": { ... },
  "sources": [ ... ],
  "requests": 1200,
  "infrastructure_requests": 8640,
  "classes": [
    { "class": "1xx", "requests": 0 }, { "class": "2xx", "requests": 1150 },
    { "class": "3xx", "requests": 18 }, { "class": "4xx", "requests": 30 },
    { "class": "5xx", "requests": 2 }, { "class": "unknown", "requests": 0 }
  ],
  "codes": [
    { "status_code": 200, "class": "2xx", "requests": 1100 }
  ]
}
```

- `classes`: always these six, in this order; `unknown` is `status_code` 0.
- `codes`: every status code with `app` requests, ascending. `requests`, `classes` and `codes`
  leave infrastructure requests out; `infrastructure_requests` counts them.

### 7.9 `GET .../api-metrics/services`

Parameters: range only (a filter is 400 `invalid_query`). Every service with any
row in the range, ordered by `service`; the list behind the service filter. An
empty `items` is the console's empty state, which links to the OTLP token page
(section 6).

```json
{
  "range": { ... },
  "items": [
    { "service": "payments-api", "source": "metric", "span_data_ignored": false,
      "requests": 1200, "infrastructure_requests": 8640, "lossy": false }
  ]
}
```

`requests` and `lossy` are over the selected source of that service.

### 7.10 `GET .../api-metrics/warnings`

Parameters: range only. Ingest warnings from `api.ingest_warnings`, which are
counted per UTC day: every day from the day of `from` to the day of the last
instant before `to`.

```json
{
  "range": { ... },
  "totals": [
    { "kind": "span_derived", "count": 0, "last_day": null },
    { "kind": "lossy_histogram", "count": 4, "last_day": "2026-11-12" },
    { "kind": "cardinality_overflow", "count": 0, "last_day": null },
    { "kind": "secret_in_path", "count": 1, "last_day": "2026-11-11" },
    { "kind": "coarse_buckets", "count": 0, "last_day": null }
  ],
  "days": [
    { "day": "2026-11-11", "kind": "secret_in_path", "count": 1 }
  ]
}
```

- `totals`: always these five kinds, in this order. `count` is the sum over the
  days, `last_day` the latest day with a count.
- `days`: every non-zero day and kind, ordered by day, then kind.
- Kinds, written by the metrics writer: `span_derived` (points derived from spans were
  stored), `lossy_histogram` (a histogram was re-bucketed inexactly),
  `cardinality_overflow` (routes or services above the daily cap were folded into
  `{other}`, or a new stream was refused by the writer's stream cap),
  `secret_in_path` (a Stellar secret seed appeared in a request path and was
  redacted), `coarse_buckets` (a histogram's buckets were much wider than the
  canonical ones, so its percentiles are not reliable). Counts are requests,
  not exports; see
  `docs/reference/api-storage.md`. The console shows a banner per kind with a
  non-zero count.

### 7.11 Configuration keys

Two keys are required for the control plane, with the same values as for
`jobs`, because the retention step of 7.2 must match what the retention job
keeps: `API_MINUTE_RETENTION_DAYS` (at least 8, the writer's late-data window) and
`API_HOURLY_RETENTION_MONTHS` (at least 24), loaded by
`config.LoadAPIRetention`.

Migration `00021_tenant_rollup_pending.sql` grants `cp_tenant` select on
`api.rollup_pending` (own rows, under the existing policy) for the freshness
rule of 7.2.

---

## 8. Global web and API views

Operators compare every enrolled project on aggregates only.
Every route here is **session** and **operator** (5.1), runs as `cp_operator`
in one read-only, repeatable-read transaction, and reads only `agg.web_daily`,
`agg.web_daily_dim`, `api.operator_daily`, `cp.web_ingest_hourly`, `cp.tenants`
and `cp.operator_sites`. `api.operator_daily(from, to)` and
`api.operator_hourly(from, to)` (migration 00022, security definer, execute for
`cp_operator` only) return day- or hour-grain rows: the rolled-up rows, and
for hours still marked in `api.rollup_pending` the same numbers computed from
their minutes, never both for one hour (the freshness rule of 7.2). API
answers therefore do not wait for `api-rollup` and do not change when it
runs. `cp_operator` cannot read `api.metrics_minute` directly, reads in `cp`
only `cp.tenants`, `cp.operator_sites`, `cp.web_ingest_hourly`, `cp.settings`,
`cp.tenant_removals` and its own key tables, cannot connect to the `umami`
database, and an operator
session holds no Umami token, so no route can return session-level rows.
Tenants with `is_internal = true` are never listed and answer 404 when named.
No new error code, field code or audit action.

Web numbers come from the `aggregate-web` jobs task, which copies Umami's own
`/stats` and `/metrics` answers per site, band and UTC day
(`docs/reference/web-metrics.md`, "Daily aggregates"). A project is the sum of
its sites. Visitors over several days are the sum of daily visitors, which is
exactly what Umami answers for the same range (measured there); a person who
visits two sites of one project counts once per site.

### 8.1 Range

Every route takes `from` and `to`, UTC days `yyyy-mm-dd`, both included.

| Given | Range |
|---|---|
| neither | the 30 days ending today (UTC) |
| only `to` | the 30 days ending `to` |
| only `from` | `from` to today |

400 `invalid_query` when a value is not a date, `to` is after today, `to` is
before `from`, or the range holds more than 366 days. Today is included and
holds what the hourly task has read so far.

### 8.2 Project filters, sort and pages

Both project lists take these filters; all given filters must hold.

| Parameter | Values | Matches |
|---|---|---|
| `status` | comma-separated subset of the five tenant statuses | `cp.tenants.status` |
| `verified` | `true`, `false` | at least one non-removed site verified by the rule of 5.8 (`verified_at` set, `last_verify_error` null), or none |
| `stellar_toml` | `true`, `false` | at least one non-removed site whose `stellar_toml.status` is `found`, or none |
| `has_web_data` | `true`, `false` | primary-band page views above 0 in the range |
| `has_api_data` | `true`, `false` | an `api.metrics_daily` row with requests in the range |
| `enrolled_from`, `enrolled_to` | `yyyy-mm-dd` | `created_at` on or after the start of `enrolled_from`, before the end of `enrolled_to` (UTC) |
| `q` | 1 to 100 characters | case-insensitive substring of `name` or `slug` |
| `program`, `round`, `category` | 1 to 80 characters | exactly the confirmed cohort value of 19.1 |
| `award_from`, `award_to` | `yyyy-mm-dd` | confirmed award date on or after, on or before |
| `integrity_at_or_below` | `A`, `B`, `C` (web list only) | the range grade of 17.3 is this grade or worse: `C` matches C, `B` matches B and C, `A` every graded project; a project without a graded day never matches |

- `sort` is any column the route lists, `order` is `asc` or `desc`
  (default `desc`). Projects without a value for the sort column (a rate
  without a denominator, a percentile without requests) come last in both
  orders. Ties are broken by `id` in the same direction. `name` sorts by
  `lower(name)`.
- `limit` and `cursor` as 2.3. The envelope adds `from`, `to` and `total`
  (projects matching the filters): `{"from", "to", "total", "items", "next_cursor"}`.
- Sort values are range sums that the hourly task changes, so no keyset
  over them stays stable. The cursor is a position in the sorted list,
  bound to the route, range, filters, sort and order it was made for; any
  other combination is 400 `invalid_cursor`. A page fetched after the task
  committed can repeat or skip a project whose numbers changed.
- 400 `invalid_query` for an unknown status, sort or order, a boolean other
  than `true` or `false`, a malformed date, `enrolled_to` before
  `enrolled_from`, `award_to` before `award_from`, `q` or a cohort value
  out of range, an `integrity_at_or_below` other than `A`, `B` or `C`, or
  `integrity_at_or_below` on the API list.
- Both lists also sort by the change and percentile columns of 19.2:
  `visitors_growth`, `pageviews_growth`, `requests_growth`,
  `error_rate_change`, `onchain_operations_growth` and
  `<metric>_percentile` for the same five metrics.

Every list row and compare project carries **TenantRef**
`{"id", "slug", "name", "status", "created_at"}`, and list rows carry the site
facts `sites`, `sites_verified`, `sites_with_stellar_toml` (non-removed sites,
rules of 5.2) and `has_web_data`, `has_api_data`.

### 8.3 `GET /api/operator/web/projects`

Operator. Default sort `pageviews`. Item:

```json
{
  "tenant": { "id": "uuid", "slug": "stellar-dex", "name": "Stellar DEX", "status": "active", "created_at": "..." },
  "sites": 2, "sites_verified": 1, "sites_with_stellar_toml": 1,
  "has_web_data": true, "has_api_data": false,
  "pageviews": 1200, "visitors": 410, "visits": 530, "bounces": 260, "totaltime": 48200,
  "bounce_rate": 0.4905660377358491, "avg_visit_duration": 90.94339622641509,
  "quarantine_pageviews": 40, "quarantine_visitors": 12, "quarantine_share": 0.03225806451612903,
  "ingest_requests": 1890, "ingest_rejected": 75, "rejected_share": 0.03968253968253968,
  "integrity": { "grade": "A", "graded_days": 24, "days_a": 22, "days_b": 2, "days_c": 0 },
  "top": {
    "referrer": [ { "key": "github.com", "value": 81 } ],
    "country": [ { "key": "DE", "value": 120 } ],
    "device": [ { "key": "laptop", "value": 300 } ],
    "browser": [ { "key": "chrome", "value": 250 } ]
  }
}
```

- `pageviews`, `visitors`, `visits`, `bounces`, `totaltime` (seconds): sums of
  the primary band over the range.
- `bounce_rate = bounces / visits`, `avg_visit_duration = totaltime / visits`
  (seconds), both `null` when `visits` is 0. These are Umami's own dashboard
  formulas.
- `quarantine_pageviews`, `quarantine_visitors`: the quarantine band.
  `quarantine_share = quarantine_pageviews / (pageviews + quarantine_pageviews)`,
  `null` when both are 0.
- `ingest_requests`: gateway outcomes in `cp.web_ingest_hourly` over the
  range, `accepted + quarantined + rejected_*`; `ingest_rejected` the
  `rejected_*` outcomes (including `rejected_gpc`, a privacy drop);
  `rejected_share = ingest_rejected / ingest_requests`, `null` when there
  were none. The replayer's `bot` and `dlq` outcomes describe accepted events
  again and are not requests.
- `top`: per dimension the 5 keys with the largest sum of daily values over
  the range (ties by key); a value is Umami's `y`, distinct visitors with a
  page view carrying that key on a day.
- Sorts: `name`, `status`, `created_at`, `sites`, `sites_verified`,
  `sites_with_stellar_toml`, `pageviews`, `visitors`, `visits`, `bounces`,
  `totaltime`, `bounce_rate`, `avg_visit_duration`, `quarantine_pageviews`,
  `quarantine_visitors`, `quarantine_share`, `ingest_requests`,
  `ingest_rejected`, `rejected_share`, `integrity`.
- `integrity`: the range grade over the project's graded site days (17.1);
  `grade` is `null` without one. Sorting by `integrity` orders A, B, C and
  puts projects without a grade last.

### 8.4 `GET /api/operator/web/compare`

Operator. Query `tenants` (1 to 5 distinct tenant ids, comma-separated),
`from`, `to` (8.1), `breakdown_limit` (1 to 500, default 10). **200**:

```json
{
  "from": "2026-11-01", "to": "2026-11-12",
  "days": ["2026-11-01", "...", "2026-11-12"],
  "projects": [
    {
      "tenant": { "TenantRef": "..." },
      "totals": {
        "pageviews": 0, "visitors": 0, "visits": 0, "bounces": 0, "totaltime": 0,
        "bounce_rate": null, "avg_visit_duration": null,
        "quarantine_pageviews": 0, "quarantine_visitors": 0, "quarantine_share": null
      },
      "series": [
        { "day": "2026-11-01", "aggregated": true, "pageviews": 31, "visitors": 12, "visits": 14,
          "bounces": 6, "totaltime": 1300, "bounce_rate": 0.42857142857142855,
          "avg_visit_duration": 92.85714285714286, "quarantine_pageviews": 2 }
      ],
      "breakdowns": {
        "referrer": { "items": [ { "key": "github.com", "value": 9 } ], "total_keys": 14 },
        "country": { "items": [], "total_keys": 0 },
        "device": { "items": [], "total_keys": 0 },
        "browser": { "items": [], "total_keys": 0 },
        "os": { "items": [], "total_keys": 0 }
      }
    }
  ]
}
```

- `projects` come in the order of `tenants`. Every `series` has one point per
  entry of `days`, so the charts align.
- `integrity`: the project's range grade with its reasons (17.1).
- A point is `aggregated: false`, with every number `null`, when no site of
  the project has a row for that day yet (the day was never read, for
  example before a backfill). A day the task read without traffic is
  `aggregated: true` with zeros.
- `totals` sum the aggregated points; derived values follow 8.3.
- `breakdowns`: referrer, country, device, browser and operating system of
  the primary band over the range, the `breakdown_limit` largest keys (ties
  by key), and `total_keys`, the number of distinct keys. For a project with
  one site, each item equals Umami's `/metrics` for the same range.
- 400 `invalid_query` when `tenants` is missing, empty, longer than 5,
  repeats an id or holds anything but lowercase UUIDs, and for a bad
  `breakdown_limit`. 404 `not_found` when an id is unknown or internal.

### 8.5 `GET /api/operator/api/projects`

Operator. Default sort `requests`. Source rows are chosen per tenant and
service exactly as 7.3 (metric rows when the service has any in the range,
otherwise span rows), over `api.metrics_daily` for the UTC days of the range.
The operator view applies the rule in SQL over every tenant at once rather
than through `apimetrics.Select`, which is tenant-scoped. Item:

```json
{
  "tenant": { "TenantRef": "..." },
  "sites": 1, "sites_verified": 1, "sites_with_stellar_toml": 0,
  "has_web_data": false, "has_api_data": true,
  "requests": 182000, "requests_4xx": 2100, "requests_5xx": 90,
  "rate_4xx": 0.011538461538461539, "rate_5xx": 0.0004945054945054945,
  "p50_seconds": 0.041, "p50_over_10s": false,
  "p95_seconds": 0.62, "p95_over_10s": false,
  "p99_seconds": null, "p99_over_10s": true,
  "services": 3, "routes": 41, "span_services": 1, "source": "mixed", "lossy": false,
  "infrastructure_requests": 260000
}
```

- `requests` is the sum of `count` over rows of traffic class `app` (7.4);
  `requests_4xx` and `requests_5xx` count status codes 400 to 499 and 500 to
  599 (rows without a status code count only in `requests`). Rates are `null`
  without requests. `infrastructure_requests` counts the rows of the other
  classes, which no other number of the item includes. Every API number of
  8.6, section 9, the exports of 16 and the views of 16.3 follows the same
  rule, and each carries its own `infrastructure_requests`.
- Percentiles use `api.histogram_quantile` over the summed buckets of the
  chosen rows, never averaged. A percentile in the open bucket
  above 10 s has `*_seconds: null` and `*_over_10s: true`; without requests
  both are `null` and `false`. Sorting puts over-10 s above every number.
- `services`: distinct services; `routes`: distinct `(service, route)` pairs
  with a non-empty route; `span_services`: services whose numbers are span
  derived; `source`: `metric` or `span` when every service uses that source,
  `mixed` when they differ, `null` without data; `lossy`: some chosen row was
  re-bucketed inexactly.
- Sorts: `name`, `status`, `created_at`, `sites`, `requests`, `requests_4xx`,
  `requests_5xx`, `rate_4xx`, `rate_5xx`, `p50`, `p95`, `p99`, `services`,
  `routes`. Sorting by `requests` in each order finds the lowest and highest
  usage projects.

### 8.6 `GET /api/operator/api/compare`

Operator. Query `tenants` (as 8.4), `from`, `to`, `service_order` (`desc`
default, or `asc`). **200**:

```json
{
  "from": "...", "to": "...", "days": ["..."],
  "projects": [
    {
      "tenant": { "TenantRef": "..." },
      "totals": { "requests": 0, "requests_4xx": 0, "requests_5xx": 0, "rate_4xx": null, "rate_5xx": null,
                  "p50_seconds": null, "p50_over_10s": false, "p95_seconds": null, "p95_over_10s": false,
                  "p99_seconds": null, "p99_over_10s": false,
                  "services": 0, "routes": 0, "span_services": 0, "source": null, "lossy": false },
      "series": [ { "day": "...", "requests": 0, "requests_4xx": 0, "requests_5xx": 0, "rate_4xx": null,
                    "rate_5xx": null, "p50_seconds": null, "p50_over_10s": false, "...": "..." } ],
      "services": [ { "service": "payments-api", "source": "metric", "requests": 0, "...": "APINumbers",
                      "routes": 12, "lossy": false } ]
    }
  ]
}
```

- `totals` equal the project's row of 8.5 for the same range.
- `series`: one point per day, the same source choice for every day (made
  over the whole range); a day without requests has 0 requests and `null`
  rates and percentiles.
- `services`: every service with its source, numbers and routes, ordered by
  requests (`service_order`), then by name, so the first and last entries
  are the highest and lowest usage services.
- Errors as 8.4.

### 8.7 Removal and `removal_keep_aggregates`

The purge step `agg` (after `api` in the removal purge of 5.5) deletes the tenant's
`agg.web_daily` and `agg.web_daily_dim` rows through the security definer
function `agg.purge_tenant`, the only right `cp_system` has in `agg`. With
`keep_aggregates` the step records `{"skipped": "keep_aggregates"}` and every
row stays (5.13). The function takes the tenant's advisory lock exclusively;
`aggregate-web` holds it shared for each write and skips removed tenants, so
rows of a purged tenant are never written again.

### 8.8 Configuration

No key of its own. `jobs` runs the task `aggregate-web` (hourly: today and the 3
days before) and `jobs run aggregate-web --from <yyyy-mm-dd> --to <yyyy-mm-dd>`
for a backfill; both read Umami with `UMAMI_URL` and `UMAMI_ADMIN_PASSWORD`,
which `jobs` already requires.

---

## 9. Operator API keys and machine metrics API

Operators create keys in the console (9.2); machines read
aggregate metrics with them under `/api/metrics/v1/` (9.3 to 9.10). The
machine API's own contract, and the document its tests validate every answer
against, is `docs/reference/openapi/metrics-v1.yaml` (OpenAPI 3.1, served at
`GET /api/metrics/v1/openapi.yaml`). This section states the rules around it;
where the two differ, the OpenAPI document is the contract of the machine API.
Usage for operators: [Operator API keys](../operators/api-keys.md).

### 9.1 Two doors that never open each other

- A key is `sa_op_` plus 40 random base62 characters. The database
  holds its SHA-256 (`cp.operator_api_keys.hash`) and the first 10 characters
  (`prefix`) for display. The key is shown once, in the create answer.
- The machine API is mounted beside `/api`, not inside it
  (`controlplane.Options.Mounts`): no session middleware, CSRF check or cookie
  handling runs there. A console session cookie on `/api/metrics/v1/` is 401.
- Every other route ignores `Authorization`: a key on any route outside
  `/api/metrics/v1/` authenticates nothing (401 or 404, as without it).
- Every machine route is `GET`; other methods are 405.

### 9.2 Key management (operator)

Every route is **session** and **operator** (5.1) and runs as `cp_system`.
Create and revoke follow the CSRF rules of 1.2.

**APIKey** object:

```json
{
  "id": "uuid", "name": "warehouse", "prefix": "sa_op_Ab3x",
  "scope": "metrics:read", "tenant_id": null, "status": "active",
  "created_by": "uuid", "created_at": "...", "expires_at": "...",
  "revoked_at": null, "last_used_at": null
}
```

- `scope` is always `metrics:read`; `tenant_id` is `null` for a key that reads
  every tenant, or the one tenant it reads (`cp.operator_api_keys.scope` is
  `global` or `tenant`).
- `status`: `revoked` when `revoked_at` is set, else `expired` when
  `expires_at` has passed, else `active`.
- `last_used_at`: the last request the key was accepted for.

| Route | Answer |
|---|---|
| `POST /api/operator/api-keys` | 201, APIKey plus `"key": "sa_op_..."`, `Location` set |
| `GET /api/operator/api-keys` | list envelope (2.3) of APIKey, newest first, keyset over `(created_at, id)`; `limit` 1 to 200 |
| `GET /api/operator/api-keys/{key_id}` | APIKey |
| `GET /api/operator/api-keys/{key_id}/usage` | 9.11 |
| `DELETE /api/operator/api-keys/{key_id}` | 204; revokes. Revoking a revoked key is 204 and changes nothing |

Create body (unknown fields are 400 `invalid_body`):

| Field | Rule | 422 field codes |
|---|---|---|
| `name` | required, 1 to 100 characters after trimming, text rules of 3.2 | `required`, `too_long`, `invalid_format` |
| `scope` | required, exactly `metrics:read` | `required`, `invalid_value` |
| `tenant_id` | optional; absent or `null` means every tenant. A tenant of any status but `removed`, internal tenants included (the external prober's key) | `invalid_format`, `unknown_tenant` |
| `expires_in_days` | optional integer 1 to 365, default 90 | `invalid_value` |

`expires_at` is `created_at` plus `expires_in_days` days, from one database
clock. Create and revoke write the audit rows of 3.3 in the same transaction.
An unknown or malformed `key_id` is 404.

### 9.3 Machine authentication, scope and limits

Every request to `/api/metrics/v1/` carries exactly one
`Authorization: Bearer sa_op_<40 base62>` header. In order:

1. Missing, repeated or malformed header, or no key with that SHA-256: 401
   `unauthenticated` with `WWW-Authenticate: Bearer realm="metrics"`. The
   lookup is by hash (an index on the hash of a 238-bit random value tells a
   timing observer nothing), and the stored hash is compared again in constant
   time.
2. `revoked_at` set or `expires_at` reached: 401 `unauthenticated`, the
   message says which. Nothing is cached: a revocation or expiry applies to the
   very next request.
3. Token bucket per key and control plane instance: 10 requests per second,
   burst 20. Empty: 429 `rate_limited` with `Retry-After` in whole seconds.
4. A key scoped to one tenant with a `tenant` parameter naming another tenant:
   403 `scope_forbidden`.
5. The request is counted, then served.

Every request of a matched key adds 1 to `requests` of its hour in
`cp.api_key_usage_hourly`; one refused in steps 2 to 4 also adds 1 to
`rejected`. An accepted request sets `last_used_at`. The count is written
before the answer; if it cannot be written the request fails (500) rather
than going uncounted.

Tenant scope on every data route (`tenant` parameter, one lowercase UUID):

| Key | `tenant` absent | `tenant` given |
|---|---|---|
| scoped to tenant X | X, whatever its status, internal included | X: as absent; another tenant: 403 |
| for every tenant | every tenant except internal ones | that tenant; 404 `not_found` when unknown or internal |

Shared query rules: a parameter the route does not list, or one given twice,
is 400 `invalid_query`. Day ranges `from`, `to` are UTC days, both included,
with the defaults and limits of 8.1 (30 days ending today, at most 366, `to`
not after today). Hour ranges are RFC 3339 hour starts (`2026-11-12T09:00:00Z`),
both included; default the 24 hours ending with the current hour; `to` not
after the current hour; at most 744 hours for `/api/hourly`, 24 for
`/web/hourly`. `service` follows 7.1 (exact match, empty matches points without
a service, at most 128 bytes, no control characters).

Lists: `limit` 1 to 500, default 500; `next_cursor` is a keyset position in
the route's order, bound to the route, the key, every other parameter and the
range it carries, and carrying the range the first page resolved (a page
fetched after midnight continues the same days). A cursor used with anything
else is 400 `invalid_cursor`. The binding hash has no secret, so the carried
range is checked again on every page with the limits of a first page (at most
366 days or 744 hours, `to` not after today or the current hour, `to` not
before `from`); a cursor whose range breaks them is 400 `invalid_cursor`.

All reads run as `cp_operator`, which reads `agg`, `api.metrics_hourly`,
`api.metrics_daily`, `cp.tenants` and `cp.operator_sites` and writes only
usage counters and `last_used_at`. The API routes (9.8 to 9.10)
read through `api.operator_daily` and `api.operator_hourly`, so hours not yet
rolled up are included as in section 8. Both functions take the set of tenants
to return as an argument (migration `00023`) and return rows of those tenants
only; callers pass the tenants of their scope, so no call returns every
tenant's rows.

### 9.4 `GET /tenants`

Tenants in scope, ordered by id: `id`, `slug`, `name`, `status`, `created_at`,
`sites`, `sites_verified`, `sites_with_stellar_toml` (the counts of 5.2) and
`verified`, `stellar_toml` (each count above 0).

### 9.5 `GET /web/daily`

Parameters `tenant`, `band` (`primary` or `quarantine`, absent for both),
`from`, `to`, `limit`, `cursor`. One item per tenant, day and band that
`agg.web_daily` holds: the sums over the tenant's sites of `pageviews`,
`visitors`, `visits`, `bounces`, `totaltime`, and `sites`, the number of site
rows summed. A day the aggregation task read without traffic has zeros; a day
it never read is absent. Ordered by tenant, day, band. Summed over a range
these equal the totals of 8.3 for the same range. `integrity` is the grade of
the tenant's sites on that day (17.4). `GET /web/integrity` lists the grades
per site and day (17.4).

### 9.6 `GET /web/dimensions`

Parameters as 9.5 plus `dimension` (`referrer`, `country`, `device`,
`browser`, `os`; absent for all). One item per tenant, day, band, dimension and
key: `value` summed over the tenant's sites (Umami's `y`, 8.3). Ordered by
tenant, day, band, dimension, key (bytewise). Summed over a range these equal
the breakdowns of 8.4.

### 9.7 `GET /web/hourly`

Parameters `tenant`, `from`, `to` (hours). A key for every tenant must give
`tenant` (400 otherwise). The control plane reads Umami's `/stats` for every
`active` site of the tenant, both websites, every hour (`[hour, hour + 1 h -
1 ms]`, because Umami includes both ends), with its Umami service account, and
returns only the sums: one item per hour and band, zeros included, with
`complete: false` for the current hour, read up to `read_at`. A site whose
website Umami does not know is a 500 (Umami answers such a website with zeros,
which would pass for no traffic); Umami unreachable is 503 `unavailable`. At
most 4 Umami requests of one answer run at once. The external prober
(`cmd/prober`) uses this route as its freshness check.

### 9.8 `GET /api/daily`

Parameters `tenant`, `service`, `from`, `to`, `limit`, `cursor`. From
`api.metrics_daily`, the source chosen per tenant and service over the whole
range (7.3; metric rows when the service has any, otherwise span rows). One
item per tenant and day with rows: `services`, `span_services`, `source`
(`metric`, `span` or `mixed`), the numbers of 8.5 (`requests`,
`requests_4xx`, `requests_5xx`, `rate_4xx`, `rate_5xx`, `p50_seconds`,
`p50_over_10s`, ... `p99_over_10s`, `infrastructure_requests`) and `lossy`. Equal to the daily series of
8.6 for the same range; with `service`, to that service's numbers of 8.6.

### 9.9 `GET /api/routes/daily`

As 9.8, one item per tenant, day, service, method and route with that
service's `source`. Ordered by tenant, day, service, method, route (bytewise).

### 9.10 `GET /api/hourly`

Parameters `tenant`, `service`, `from`, `to` (hours), `limit`, `cursor`. As
9.8 over `api.metrics_hourly` (item field `hour`), which the rollup refreshes
every 5 minutes, so the newest minutes of the current hour can be missing.

### 9.11 `GET /api/operator/api-keys/{key_id}/usage`

Operator. Parameters `from`, `to` (UTC days, default the 7 days ending today,
at most 31). Answer: `key_id`, `from`, `to`, `totals` `{requests, rejected}`
and `hours`, every hour with a row in `cp.api_key_usage_hourly`:
`{hour, requests, rejected}`.

### 9.12 `GET /rules` and `GET /rules/{rule_id}/matches`

The saved threshold rules and the tenants that meet one, as in 19.4. `/rules`
takes `tenant` only to check it like every route; the rule list holds no
tenant data. `/rules/{rule_id}/matches` takes `tenant` and `as_of`; a key
scoped to one tenant evaluates that tenant only. Neither route has pages.

### 9.13 Configuration

No key of its own. The rate limit (10 per second, burst 20), page size (500),
key lifetime (90 days default, 365 maximum) and range limits are constants of
`internal/controlplane/operator`.

---

## 10. Erasure

Tenant-triggered erasure. The workflow and its limits are described for people
in [Right to erasure](../compliance/erasure.md); this section is the contract. Code: `internal/controlplane/erasure`, jobs task
`internal/jobs/erasurefollowup`.

### 10.1 Erasure request object

```json
{
  "id": "uuid",
  "tenant_id": "uuid",
  "site_id": "uuid",
  "kind": "visitor_today",
  "status": "done",
  "outcome": "erased",
  "message": "Deleted 2 session(s) with 4 event(s) of this visitor on 2026-11-12 (UTC) and 0 undeliverable queued message(s).",
  "day": "2026-11-12",
  "session_id": null,
  "requested_by": "uuid",
  "requested_at": "...",
  "completed_at": "...",
  "sessions_deleted": 2,
  "events_deleted": 4,
  "dlq_messages_deleted": 0,
  "affected_from": "2026-11-12",
  "affected_to": "2026-11-12",
  "reaggregated_at": null,
  "error": null
}
```

- `kind`: `visitor_today`, `session` or `site_reset`.
- `status`: `running`, then `done` or `failed` (`pending` exists in the column
  check but is not used).
- `outcome` (only when `done`): `erased`, or `nothing_found` when the request was
  valid but matched no data. `message` explains the result in English; clients
  show it as is.
- `day`: `visitor_today` only, the UTC day whose secret produced the token (the
  gateways' day, not the client's).
- `session_id`: `session` only, the Umami session id supplied.
- `affected_from`, `affected_to`: UTC days whose aggregates the jobs task reads
  again; null when nothing was deleted. `reaggregated_at` is set when that is done.
- `error` (only when `failed`): why; counts still show what was deleted before
  the failure.
- No field ever holds the IP address, the user agent or the visitor token.

### 10.2 `POST /api/tenants/{tenant_id}/erasure`

Session, owner (403 `owner_required` for members). Allowed in every tenant
status a member can reach (suspended included) and without current terms.

Body, one of:

```json
{ "kind": "visitor_today", "site_id": "uuid", "ip": "203.0.113.7", "user_agent": "Mozilla/5.0 ..." }
{ "kind": "session", "site_id": "uuid", "session_id": "uuid" }
{ "kind": "site_reset", "site_id": "uuid", "confirm_domain": "example.org" }
```

Validation (422 with `fields`): `kind` required, one of the three
(`invalid_value`); `site_id` required, a lowercase uuid of a site of the tenant
(`invalid_format`, `invalid_value`); a field of another kind is refused
(`invalid_value`); `ip` required for `visitor_today`, an IPv4 or IPv6 address
without zone (`invalid_format`); `user_agent` required, at most 2,048 bytes, no
control characters (`too_long`, `invalid_format`); `session_id` a lowercase uuid;
`confirm_domain` equal to the site's `domain`. Error messages never quote `ip`
or `user_agent`.

Other answers: 409 `site_removed`; 503 `provisioning_pending` while the site's
Umami websites do not exist; 503 `unavailable` when no gateway answers the
visitor-token request or the event queue is unreachable; 409
`erasure_in_progress` while another request of the same site is `running`.

Success: **202** with the request object, `status = "running"`. The work
continues in the control plane; clients poll 10.4.

### 10.3 `GET /api/tenants/{tenant_id}/erasure`

Session, member. List envelope (2.3), newest first by `(requested_at, id)`.
Optional query `site_id` filters to one site.

### 10.4 `GET /api/tenants/{tenant_id}/erasure/{request_id}`

Session, member. The request object; 404 for an unknown id or one of another
tenant.

### 10.5 What a run does

1. `visitor_today`: before the row is written, the control plane sends
   `{site, addr, agent}` on `edge.visitor-token`; a gateway answers
   `{vt, day}` (`internal/gateway/visitor.go`). The address and user agent are
   dropped after this request.
2. The control plane records the last sequence of `WEB_EVENTS`, inserts the row
   (`running`, `heartbeat_at`), writes `erasure.request` and answers 202.
3. The run renews `heartbeat_at` every 10 seconds and waits until `WEB_EVENTS`
   holds no message on `web.events.<site>` at or below the recorded sequence. If
   that takes longer than `ERASURE_QUEUE_WAIT`, it fails with nothing deleted.
4. `visitor_today`: reads `visit.<site>.<vt>` in `visit_state`, purges it, and
   deletes each Umami session it names (both bands); deletes `WEB_DLQ` messages
   on `dlq.web.<site>` that contain the token (secure delete).
   `session`: deletes the session from whichever band has it, then finds visit
   records naming it, purges them and deletes their `WEB_DLQ` messages.
   `site_reset`: reads Umami's session stats for the counts, resets both Umami
   websites, deletes every `WEB_DLQ` message and visit record of the site.
5. The row becomes `done` (or `failed`) with counts and affected days, and
   `erasure.complete` (or `erasure.fail`) is written.
6. The jobs task `erasure-followup` (every minute) fails `running` rows whose
   heartbeat is older than 2 minutes, then for each finished row with
   affected days reads those days of `agg.web_daily` and `agg.web_daily_dim`
   again from Umami; for `site_reset` it deletes every aggregate row of the site
   and reads the last 4 days again. It sets `reaggregated_at`.

NATS rights this needs (`internal/natsx/topology.go`): the control plane sends
on `edge.visitor-token`, writes `visit_state`, reads `WEB_EVENTS` (stream info
and message get) and manages `WEB_DLQ`.

### 10.6 Configuration

| Key | Service | Meaning |
|---|---|---|
| `ERASURE_QUEUE_WAIT` | controlplane | optional, Go duration, default `15m`, at most `72h`: how long a run waits for the site's queued events |
| `WEB_RAW_RETENTION_MONTHS` | `jobs retain-web` | optional, default 25, 1 to 1200 |
| `UMAMI_DB_NAME`, `UMAMI_DB_JOBS_RETENTION_PASSWORD` | `jobs retain-web`, `platformctl db umami-retention-role` | the umami database on the platform cluster (with `PLATFORM_DB_HOST`, `PLATFORM_DB_PORT`, `PLATFORM_DB_SSLMODE`) and the password of role `jobs_retention` |

## 11. Operator alerts

The console's Alerts page (`/operator/alerts`) shows what the Alertmanager
cluster holds right now. It is the operators' alert channel, so the route never
lets the page say "all clear" without having asked Alertmanager.

### 11.1 `GET /api/operator/alerts`

Operator (5.1). No query parameters. The control plane asks every configured
Alertmanager member at once (`GET /api/v2/alerts` without filters, 5 seconds
per member) and merges the answers.

```json
{
  "checked_at": "2026-11-13T12:00:05Z",
  "watchdog_firing": true,
  "sources": [
    { "address": "10.0.1.4:9093", "reachable": true, "watchdog_firing": true, "error": null },
    { "address": "10.0.1.5:9093", "reachable": false, "watchdog_firing": false,
      "error": "dial tcp 10.0.1.5:9093: connect: connection refused" }
  ],
  "alerts": [
    { "fingerprint": "5f1c0e7d2a9b3c41", "name": "ServiceDown", "severity": "page",
      "summary": "controlplane on core-2 does not answer scrapes", "description": null,
      "node": "core-2", "instance": "10.0.1.5:9303", "service": "controlplane",
      "starts_at": "2026-11-13T09:55:00Z", "state": "active", "inhibited_by": [],
      "runbook": "docs/runbooks/alerts.md#servicedown",
      "labels": { "alertname": "ServiceDown", "severity": "page", "node": "core-2",
                  "job": "controlplane", "instance": "10.0.1.5:9303" } }
  ]
}
```

- `checked_at`: when the control plane asked.
- `sources`: one entry per member of `ALERTMANAGER_URLS`, in that order.
  `error` is the reason a member gave no usable answer (connection, TLS, an
  HTTP status other than 200, a body that is not a list of alerts carrying
  the fields the v2 API documents as required), else `null`.
- `alerts`: every alert that has not resolved, without silenced alerts (a
  non-empty `silencedBy`) and without `Watchdog`. Prometheus sends every
  alert to every member, so one fingerprint seen by several members is one
  entry: the first member in configured order gives its fields, and
  `starts_at` is the earliest of them. Ordered by severity (`page`, then
  `ticket`, then any other severity by name, then none), then `starts_at`
  ascending (longest firing first), then `name` and `fingerprint`.
- `name` is the `alertname` label. `severity`, `node`, `instance` are labels
  of those names, `service` is the label `job` (the scrape job, which names
  the service in every rule), `summary`, `description` and `runbook` are the
  annotations of those names; each is `null` when absent. `labels` holds every
  label.
- `state` is Alertmanager's: `active`; `suppressed` (inhibited by another
  firing alert, named in `inhibited_by` by fingerprint, or muted);
  `unprocessed` (received, not yet processed).
- `watchdog_firing` is `true` when at least one member that answered holds
  `Watchdog` (`infra/ansible/roles/observability/files/rules/platform.yml`),
  silenced or not; per member in `sources`. `Watchdog` always fires, so
  `false` means the pipeline from Prometheus to Alertmanager is down
  (Prometheus not evaluating rules, or unable to reach Alertmanager) and
  `alerts` can be missing alerts. The console shows that as "alerting
  pipeline down" and never as all clear.

### 11.2 Errors

- 503 `alert_source_unreachable` when no member answered. The message names
  each member and its reason. There is no list in this case: an empty list
  would read as all clear.
- 503 `alert_source_not_configured` when `ALERTMANAGER_URLS` is unset.
- As every operator route: 401 without a session, 403 `operator_required`
  for everyone else, and the route then never asks Alertmanager.

### 11.3 Configuration

| Key | Required | Meaning |
|---|---|---|
| `ALERTMANAGER_URLS` | no | comma-separated base URLs of every Alertmanager member, `http://` or `https://`, no path. Unset means 11.2 `alert_source_not_configured` |
| `ALERTMANAGER_TLS_CA` | for `https://` | the internal CA that signs the members' certificates; the system roots are never used. Set together with `https://` URLs only |
| `ALERTMANAGER_TLS_CERT`, `ALERTMANAGER_TLS_KEY` | when the members ask for a client certificate | the client certificate, which every member of a deployment asks for |

The core role (`infra/ansible/roles/core`) sets all four: every node of the
`core` group on its private address at `core_alertmanager_port` (9093), and
the node certificate installed for the control plane's user
(`sa_tls_consumers`, uid 65532).


---

## 12. MCP server

The control plane serves a Model Context Protocol server, so any MCP client
(desktop and command-line assistants, editors, and the AI vendors' web
connectors) can read a project's web and API analytics, and operators the
global view. Code: `internal/controlplane/mcpserver`, migration
`00030_mcp.sql`. Guides: [AI assistants](../onboarding/mcp.md) for projects
and [AI assistants for operators](../operators/mcp.md). Umami's own MCP endpoint stays off and
blocked by the web Caddy (`/mcp`, `api/me/api-keys`): its keys live outside
the control plane, which could then not revoke them on the events of 12.2.

### 12.1 Transport

| Item | Rule |
|---|---|
| Endpoint | `https://app.<BASE_DOMAIN>/mcp`, routed by `deploy/caddy/app.Caddyfile` to the control plane beside `/api`. Dev: `http://localhost:5173/mcp` through the console's Vite proxy |
| Transport | Streamable HTTP of the official Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.8.0), stateless and with JSON answers: every request carries its credential and no session is kept, so any control plane instance answers any request |
| Protocol | the SDK's versions; `2026-07-28` is negotiated with clients that support it, `2025-11-25` and earlier with the others |
| Methods | `POST /mcp` only; `GET` and `DELETE` are 405 (no server stream, no session) |
| Origin | a request with an `Origin` header is 403 `origin_mismatch` (the transport's DNS rebinding rule). No browser page is a client: native clients and the vendors' connector servers send none. No CORS header is sent on `/mcp`, `/oauth/token`, `/oauth/register` or `/oauth/revoke` |
| Cookies, CSRF | none: the session cookie means nothing on `/mcp`, and the CSRF rules of 1.2 do not apply there |
| Body | at most 64 KiB per JSON-RPC request |
| Errors outside tools | the error envelope of 2.2 (401 `unauthenticated`, 403, 429 `rate_limited` with `Retry-After`) |

### 12.2 Credentials and the principal

Every request carries exactly one `Authorization: Bearer <credential>`:

- an **MCP access key**, `sa_mcp_` plus 40 random base62 characters, made in
  the console (12.8) and shown once; the database holds its SHA-256 and the
  first four random characters for display;
- an **OAuth access token**, `sa_mcpat_` plus 40 characters, from the flow of
  12.3.

Operator API keys (`sa_op_`, section 9) are not accepted: 9.1 keeps them to
the machine API, and an operator who wants the operator tools makes an
operator-scope MCP key (12.8), which acts as that operator and ends with the
operator's rights.

Both are rows of `cp.mcp_credentials` (`kind` `key` or `oauth`) and act as
one console user: the user who made the key or approved the grant. A
`tenant` credential reads one tenant with that member's role; an `operator`
credential reads the operator tools. On every request, in one `cp_system`
statement, the control plane reads the credential, its user, the membership
and the tenant status. Refused with 401 and
`WWW-Authenticate: Bearer resource_metadata="<12.3 document>", scope="analytics:read", error="invalid_token"`:

| Case | Message |
|---|---|
| missing, repeated or malformed header; unknown key or token | `unknown ...` or the format message |
| revoked | `this credential was revoked` |
| key past `expires_at`, access token past its hour | `... expired` |
| issued for another resource URI (12.3 audience) | `... another resource` |
| tenant credential whose user is not a member any more | `... no longer a member` |
| tenant status `suspended`, `rejected` or `removed` | `the project of this credential is <status>` |
| operator credential whose user lost the operator flag (suspended or removed from the roster) | `... no longer an operator` |

**Automatic revocation.** Triggers of migration 00030 set `revoked_at` and
`revoked_reason` and write one audit row per credential in the transaction of
the change: a member removed (`member_removed`), a tenant suspended, rejected
or removed (`tenant_suspended`, `tenant_rejected`, `tenant_removed`), a
user's `is_operator` turned off (`operator_removed`). A deleted user row
deletes its credentials (`on delete cascade`). Revocation is final: resuming a
suspended tenant or restoring a flag does not restore a credential. The
per-request check above covers the same events, so nothing waits for a
trigger or a cache.

`last_seen_at` is stamped at most once a minute per credential.

### 12.3 OAuth 2.1 authorization server

The control plane is its own authorization server, as the MCP authorization
specification describes: the issuer is `CONSOLE_ORIGIN`, the protected
resource is `<CONSOLE_ORIGIN>/mcp`.

| Path | Standard | Notes |
|---|---|---|
| `GET /.well-known/oauth-protected-resource/mcp` and `GET /.well-known/oauth-protected-resource` | RFC 9728 | `resource`, `authorization_servers: [issuer]`, `scopes_supported: ["analytics:read"]`, `bearer_methods_supported: ["header"]`; `Access-Control-Allow-Origin: *` |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 | endpoints below, `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`, `grant_types_supported: ["authorization_code", "refresh_token"]`, `authorization_response_iss_parameter_supported: true`, `client_id_metadata_document_supported: false` |
| `POST /oauth/register` | RFC 7591 | public clients only (`token_endpoint_auth_method` absent or `none`); 1 to 10 `redirect_uris`, each `https`, `http` on a loopback host (RFC 8252 7.3) or a private-use scheme without a fragment (`javascript`, `data`, `file` and similar refused); `client_name` at most 100 characters. Unknown metadata is ignored. 201 with `client_id` (`mcpc_` and 32 characters). At most 10 registrations a minute per instance, burst 30; a client without any grant is deleted after a day |
| `GET /oauth/authorize` | OAuth 2.1 | `response_type=code`, `client_id`, `redirect_uri` (exactly a registered one; optional when the client registered exactly one), `code_challenge` with `code_challenge_method=S256` (required), `state` (at most 1,024 characters), optional `resource` (must equal the resource URI, RFC 8707) and `scope` (`analytics:read`; `offline_access` is accepted and ignored, every grant gets a refresh token). An unknown client or redirect URI is shown on the console page with `error`; every later error goes to the redirect URI with `error`, `state` and `iss`. A valid request is sealed (AES-256-GCM, key derived from `OAUTH_STATE_KEY` with HKDF and its own label, 10 minutes) and the browser goes to the console page `/ai/consent?request=<sealed>` |
| `POST /api/mcp/authorize/inspect`, `.../approve`, `.../deny` | console | session routes with the CSRF rules of 1.2 (12.8) |
| `POST /oauth/token` | OAuth 2.1 | form body. `authorization_code`: `client_id`, `code` (single use, 60 seconds), `redirect_uri` (as in the request), `code_verifier` (43 to 128 unreserved characters, S256 checked), optional `resource`. `refresh_token`: `client_id`, `refresh_token`. 200 `{access_token, token_type: "Bearer", expires_in: 3600, refresh_token, scope}` with `Cache-Control: no-store` |
| `POST /oauth/revoke` | RFC 7009 | `client_id`, `token` (refresh or access token); revokes the whole grant of the presenting client; 200 whatever the token was |

Lifetimes: access token 1 hour, refresh token 30 days from its issue, rotated
on every use. A refresh token used twice revokes its grant
(`refresh_reuse`), and an authorization code used twice revokes the grant the
first use made (`code_reuse`). At token time, both grants check membership,
tenant status and the operator flag again. Token errors follow RFC 6749 5.2:
`invalid_request`, `invalid_client` (401), `invalid_grant`,
`unsupported_grant_type`, `invalid_target`.

Consent: the console page `/ai/consent` signs the person in with GitHub if
needed (the sealed request waits in the tab's session storage), shows the
client name, where the browser returns and what is granted, and lists the
person's projects with status `active` or `pending`; operators also see "the
operator view". Approve writes the code and answers `{redirect_to}`, which the
page follows; deny answers the redirect with `error=access_denied`.

### 12.4 Tools

Every tool is read-only (`readOnlyHint: true`, `openWorldHint: false`), takes
a JSON object whose schema refuses unknown properties, and answers structured
content (also as JSON text). A tenant credential sees the tenant tools only,
an operator credential the operator tools only.

Shared input rules:

- Dates are ISO 8601: a UTC day `2026-11-01` or an RFC 3339 timestamp with a
  zone. A day as the end of a web range means the end of that day.
- Web ranges: `from` and `to` optional, default the 7 days ending now; `from`
  not before 2000, `to` at most a day ahead, at most 366 days. `unit`
  `minute` is for ranges up to a day, `hour` up to 31 days. `timezone` is an
  IANA name, default UTC.
- API ranges are those of 7.1 and 7.2 (`range` presets or `custom` with
  `from` and `to`), operator ranges those of 8.1 (UTC days, at most 366).
- Site ids, tenant ids, funnel and goal ids are lowercase UUIDs.
- Lists page with the console's own `limit` and `cursor` (1 to 200), Umami
  lists with `page` (1 to 10,000) and `page_size` (1 to 100), rankings with
  `limit` (1 to 500) and `offset`.
- Text filters are UTF-8 without control characters, at most 500 bytes for
  web filters and the column limits of 7.1 for API filters.

**12.4.1 Project tools** call the console's own read routes in process as the
credential's user (`tenants.Routes`, `sites.Routes`, `apimetrics.Routes`):
membership under `cp_system`, the read under `cp_tenant` with row-level
security, exactly as for the console. Only `GET` reaches them.

| Tool | Reads |
|---|---|
| `list_projects` | 4.2.3 of the credential's tenant |
| `list_sites`, `get_site` | 4.3.2, 4.3.3 |

**12.4.2 Web analytics tools** read Umami's REST API on the private network as
the credential user's own Umami user, the login of the SSO route (4.4.1,
daily derived password, a 5 minute login cache per user and site, a 401 drops
the entry). A `site_id` from the client is first read through 4.3.3 as the
principal, which answers only for a site of the credential's tenant; only
then is the Umami website id derived from it (`view` `primary` is the site
id, `quarantine` its `quarantine_website_id`). Saved funnel and goal ids are
checked against the site's own list before use, besides Umami's own check.
`internal/umami.ReadWebsite` allows exactly these read routes:

| Tool | Umami route |
|---|---|
| `get_web_stats` | `/stats` with `compare` `prev` or `yoy`; adds bounce rate, average visit seconds and page views per visit |
| `get_web_traffic` | `/pageviews` |
| `get_web_metrics` | `/metrics`, `type` one of path, entry, exit, title, query, hostname, referrer, domain, channel, event, tag, browser, os, device, screen, language, country, region, city, utmSource, utmMedium, utmCampaign, utmContent, utmTerm |
| `get_web_realtime` | `/active` |
| `get_event_stats`, `get_event_series` | `/events/stats`, `/events/series` |
| `get_event_properties` | `/event-data/properties` with `/event-data/stats`, or `/event-data/values` |
| `get_sessions`, `get_session_stats` | `/sessions`, `/sessions/stats` |
| `list_funnels`, `run_funnel` | `/funnels`, `/funnels/stats` or `/funnels/<id>/stats` |
| `run_journey`, `run_retention` | `/journeys`, `/retention` |
| `get_goals` | `/goals`, and `/goals/<id>/stats` with `with_results` |

Filters: path, referrer, title, query, hostname, event, tag, browser, os,
device, country, region, city, language, `utm_*` and `match`. Distinct ids,
segments, cohorts, revenue and Web Vitals are not offered: the pinned tracker
sends no distinct id and no Web Vitals, and no project defines segments or
revenue, so those tools would only answer zeros.

**12.4.3 API analytics tools** call 7.5 to 7.10 in process:
`list_api_services` (7.9), `get_api_overview` (7.5), `get_api_timeseries`
(7.6), `get_api_status_codes` (7.8), `list_api_routes` (7.7),
`get_api_top_error_routes` (7.7 sorted by `errors_5xx`, `rate_5xx`,
`errors_4xx` or `rate_4xx`) and `get_api_warnings` (7.10). Answers carry a
`note` that the percentiles are estimates from histogram buckets (7.4).

**12.4.4 Operator tools** call the operator routes in process behind the
guard of 5.1: `list_tenants` (5.3, search and filters), `get_tenant` (5.4),
`get_global_web_overview` (8.3), `get_global_api_overview` (8.5),
`compare_tenants_web` (8.4) and `compare_tenants_api` (8.6), with their
filters (the cohort filters of 19.1 included), sorts and pages,
`get_tenant_integrity` (17.3), `list_cohorts` (19.1), `list_threshold_rules`
and `get_threshold_rule_matches` (19.4).

**12.4.5 Integrity** for a project: `get_web_integrity` (17.2).

No MCP resources or prompts are offered; the server's instructions tell a
model the order to call tools in.

### 12.5 Tool errors

A refused call answers `isError: true` with
`{"error": {"code", "message"}}` and no stack trace or internal detail:
`invalid_input` (the schema, a range, a filter, a cursor, a console 4xx),
`not_found` (an id that is not the tenant's, including another tenant's
site), `forbidden`, `provisioning_pending`, `rate_limited`, `unavailable`
(Umami or the database), `timeout`, `result_too_large`, `internal`. An
unknown tool, including an operator tool called with a tenant credential, is
a JSON-RPC error.

### 12.6 Limits

| Limit | Value |
|---|---|
| Requests per credential and instance | 5 per second, burst 30 (429 with `Retry-After`) |
| Tool calls running per credential | 4 |
| One tool call | 30 seconds |
| One tool result | 512 KiB of JSON |
| Access keys per user and project, per operator | 20 active |
| Key lifetime | 90 days by default, 1 to 365 |

### 12.7 Audit and metrics

- Every tool call writes one `cp.audit_log` row before the answer: actor
  `mcp_credential:<id>`, action `mcp.tool_call`, target `tenant:<id>` (empty
  for operator calls), detail `{tool, outcome, duration_ms, kind, scope,
  user_id, tenant_id, credential_id}`. Never the arguments or the result. A
  call whose row cannot be written answers `internal`.
- Metrics on the ops port: `controlplane_mcp_requests_total{outcome}`,
  `controlplane_mcp_tool_calls_total{tool, outcome}`,
  `controlplane_mcp_tool_call_duration_seconds{tool}`,
  `controlplane_mcp_oauth_requests_total{endpoint, outcome}`. Alert
  `MCPToolFailuresHigh` ([alert runbook](../runbooks/alerts.md)).

### 12.8 Console routes

Session routes with the CSRF rules of 1.2.

**Credential** object:

```json
{
  "id": "uuid", "kind": "key", "scope": "tenant", "tenant_id": "uuid",
  "name": "laptop", "prefix": "sa_mcp_Ab3x", "client_id": null,
  "user_id": "uuid", "github_login": "octocat", "status": "active",
  "created_at": "...", "expires_at": "...", "revoked_at": null,
  "revoked_reason": null, "last_seen_at": null
}
```

`kind` `oauth` rows carry the client's name as `name`, its `client_id`, no
`prefix` and `expires_at` null. `status` is `revoked`, else `expired`, else
`active`.

| Route | Who | Answer |
|---|---|---|
| `GET /api/tenants/{tenant_id}/mcp-credentials` | member (1.3) | list envelope of Credential, keys and OAuth grants of the tenant, newest first, keyset over `(created_at, id)`, under `cp_tenant` |
| `POST /api/tenants/{tenant_id}/mcp-credentials` | member | body `{name, expires_in_days}` (name 1 to 100 characters after trimming, the text rules of 3.2; days 1 to 365, default 90). 201 Credential plus `"key"`, shown once. 409 `tenant_suspended` or `tenant_not_active` (rejected), 409 `too_many_keys` over 20 active keys of the user. The key acts as the caller; a trigger refuses any other user |
| `DELETE /api/tenants/{tenant_id}/mcp-credentials/{credential_id}` | the credential's user, or an owner | 204, `revoked_reason` `user`; 403 `owner_required` for another member's credential; repeating is 204 |
| `GET /api/operator/mcp-credentials` | operator | operator-scope credentials |
| `POST /api/operator/mcp-credentials` | operator | as the tenant create, `scope` `operator` |
| `DELETE /api/operator/mcp-credentials/{credential_id}` | operator | 204, reason `operator`; any operator may revoke any operator credential |
| `POST /api/mcp/authorize/inspect` | session | body `{request}`; `{client_name, client_id, redirect_origin, scope, resource, expires_at, tenants: [{id, slug, name, status, role}], operator}` |
| `POST /api/mcp/authorize/approve` | session | body `{request, scope: "tenant" or "operator", tenant_id}`; 200 `{redirect_to}`; 404 for a tenant the user may not grant, 403 `operator_required` for the operator view without the flag, 422 for a tampered or expired request |
| `POST /api/mcp/authorize/deny` | session | body `{request}`; 200 `{redirect_to}` with `error=access_denied` |

Audit actions: `mcp_credential.create` (actor `user:<id>`; detail `kind`,
`scope`, `tenant_id`, `name`, `prefix` or `client_id`), `mcp_credential.revoke`
(actor `user:<id>`, `system:mcp-revoke` for the triggers or
`system:mcp-oauth`; detail `kind`, `scope`, `tenant_id`, `user_id`, `reason`),
`mcp_oauth.consent` (target `mcp_client:<id>`), `mcp.tool_call` (12.7).

### 12.9 Configuration

No new key: the issuer is `CONSOLE_ORIGIN` and the seal key is derived from
`OAUTH_STATE_KEY`. The limits of 12.6 are constants of
`internal/controlplane/mcpserver`.

## 15. On-chain witness

A project declares the Stellar accounts (`G...`) and Soroban contracts
(`C...`) it operates, proves that it controls them, and gets their daily
ledger activity next to its web and API usage. Ledger data is public. The
platform stores the declared ids, the proofs, three daily counts per id and
one weekly count per project; it stores no counterparty address and no
transaction, and keeps the accounts behind a weekly count only as keyed
hashes until the week closes (15.8).

### 15.1 Tables

- `cp.chain_ids` (migration 00032): one row per declared id and project,
  unique on `(tenant_id, strkey)`. `kind` is `account` or `contract`,
  `added_by` is `enrollment` or `console`. A trigger on `cp.tenants` copies the
  G and C ids of `enrollment_answers.stellar_ids` into it at enrollment and
  resubmission; an id the answers already held and the tenant removed in the
  console is not copied again until the answers add it anew. M (muxed) ids
  are not copied: their activity is the activity of their G account, which the
  tenant adds. `cp_tenant` inserts and deletes rows of its own tenant; only
  `cp_system` writes the challenge and proof columns.
- `agg.chain_daily`: one row per declared id and completed UTC day that the
  jobs task `chain-activity` read, zeros included: `operations`,
  `transactions`, `counterparties` and `source`. A day without a row was not
  read; it is never a zero.
- `agg.chain_reads`: the read state per id: `covered_from` and `covered_to`
  (first and last day with a row), `last_success_at`, `last_error` with
  `last_error_at`, and for accounts `home_domain` and `account_exists` from
  the account's ledger entry.

- `agg.chain_weeks` and `agg.chain_week_members` (migration 00038): the
  weekly active accounts of 15.8.

`agg.chain_daily` and `agg.chain_reads` are readable by `cp_tenant` (own
rows, row-level security), `cp_operator` and `jobs` (writes).
`agg.chain_weeks` is readable the same way without its `hash_key` column;
`agg.chain_week_members` is the jobs task's alone. The purge step `chain` of
5.5 deletes the read state, the week members and the open weeks of a removed
tenant and, unless `removal_keep_aggregates`, its daily counts and closed
weeks, through `agg.purge_chain(tenant, keep_aggregates)`.

### 15.2 Sources and what the counts mean

| Kind | Source | Operations | Transactions | Counterparties |
|---|---|---|---|---|
| account | Horizon, `GET /accounts/{id}/operations`, successful operations only | operations that involve the account (as source or as a named party) | distinct transactions of those operations | distinct other G accounts named by them (source, from, to, funder, account, into, trustor, trustee, claimant, sponsor, seller, buyer) |
| contract | Stellar RPC `getEvents` with a contract filter, events of successful calls | distinct (transaction, operation) pairs with an event of the contract | distinct transactions with an event of the contract | distinct G and C addresses named in the event topics, other than the contract |

- `STELLAR_HORIZON_URL` (default `https://horizon.stellar.org`) and
  `STELLAR_RPC_URL` (default `https://mainnet.sorobanrpc.com`, a provider from
  the Stellar developer documentation's provider list) set the sources, with
  `STELLAR_HORIZON_RPS` (default 0.8) and `STELLAR_RPC_RPS` (default 2) as the
  request rate of each. The control plane and the jobs process read the same
  keys.
- Horizon of the Stellar Development Foundation keeps about one year of
  history. The first read of an account covers the 90 days before today.
- Stellar RPC servers keep events for a window of 120,960 ledgers, about seven
  days. A contract's history starts with the first whole UTC day of that
  window at its first read; days before it are `before_rpc_retention`. A
  contract that emits no events shows no activity through this source.
- A day is stored only when it was read whole. An id with more than 20,000
  operations (accounts) or 50,000 events (contracts) to read in one run keeps
  the days it completed and records `day_over_read_cap`.

### 15.3 Routes for members

All routes take a session (section 1) and answer 404 for a tenant the caller
is not a member of.

**`GET /api/tenants/{tenant_id}/onchain?from=&to=`**: the view. `from` and `to`
are UTC days, both included, default the 30 days ending today, at most 366
days, `to` not after today (400 `invalid_query` otherwise).

```json
{
  "from": "2026-10-18", "to": "2026-11-16", "days": ["2026-10-18", "..."],
  "sources": { "accounts": "horizon", "contracts": "rpc_events", "account_backfill_days": 90 },
  "totals": { "ids": 2, "ids_verified": 1, "accounts": 1, "contracts": 1,
              "operations": 412, "transactions": 180, "days_missing": 9,
              "weekly_active_accounts": { "average": 37.5, "latest": 41,
                                          "weeks_counted": 2, "weeks_missing": 1 } },
  "ids": [
    {
      "id": "uuid", "strkey": "G...", "kind": "account", "added_by": "enrollment",
      "created_at": "2026-11-12T09:00:00Z",
      "verification": { "status": "verified", "method": "sep53", "verified_at": "...",
                        "via": null, "detail": { "standard": "sep-53" },
                        "last_attempt_at": "...", "last_error": null },
      "read": { "source": "horizon", "covered_from": "2026-08-18", "covered_to": "2026-11-15",
                "last_success_at": "...", "last_error": null, "last_error_at": null,
                "home_domain": "example.org", "account_exists": true },
      "totals": { "operations": 412, "transactions": 180, "peak_counterparties": 31,
                  "days_read": 29, "days_missing": 1 },
      "series": [
        { "day": "2026-10-18", "operations": 12, "transactions": 5, "counterparties": 3, "missing": null },
        { "day": "2026-11-16", "operations": null, "transactions": null, "counterparties": null,
          "missing": "day_not_complete" }
      ]
    }
  ],
  "weeks": [
    { "week": "2026-W44", "from": "2026-10-26", "to": "2026-11-01", "active_accounts": 34, "missing": null },
    { "week": "2026-W45", "from": "2026-11-02", "to": "2026-11-08", "active_accounts": null,
      "missing": "day_over_read_cap" },
    { "week": "2026-W46", "from": "2026-11-09", "to": "2026-11-15", "active_accounts": 41, "missing": null }
  ],
  "link": { "two_way": true, "sites": ["LinkSite, 15.5"] }
}
```

- A point has numbers or a `missing` reason, never both: `day_not_complete`
  (today), `before_backfill_window` (an account day before its first read
  day), `before_rpc_retention` (a contract day before its first read day),
  `not_read_yet`, or the id's `read.last_error` (`rate_limited`,
  `source_unavailable`, `bad_response`, `before_source_history`,
  `day_over_read_cap`, `account_not_found`, `not_found`).
- Counterparties are distinct per day, so a range carries
  `peak_counterparties`, the largest daily value, and never a sum.
- `totals.days_missing` counts id-days without a row, today included.
- `verification.status` is `verified` once a proof held; a later failed
  attempt keeps it and sets `last_error`.
- `weeks` lists the ISO weeks that lie whole inside the range, with their
  weekly active accounts (15.8); `totals.weekly_active_accounts` sums them.

**`POST /api/tenants/{tenant_id}/onchain/ids`** `{"strkey": "G..."}`: declares
an account or a contract. Any member, terms accepted (403
`terms_not_accepted`), not while the tenant is `suspended` (409
`tenant_suspended`). 201 with the id object (the fields `id` to
`verification` of a view item). 422 `validation_failed` with
`fields.strkey`: `required`, `invalid_strkey` (not a valid G or C strkey; M
is refused), `duplicate`, `too_many` (20 ids per project).

**`DELETE /api/tenants/{tenant_id}/onchain/ids/{chain_id}`**: owner only (403
`owner_required`), terms accepted, not `suspended`. 204. The id's daily rows
stay and come back if the id is added again.

### 15.4 Proof of control

**`POST /api/tenants/{tenant_id}/onchain/ids/{chain_id}/challenge`** (accounts
only, 422 for a contract): any member. A new random nonce valid for 30
minutes replaces any earlier one.

```json
{
  "id": "uuid", "account": "G...",
  "message": "Veristar ownership proof\nProject: <tenant id>\nAccount: G...\nNonce: <32 hex>\nExpires: 2026-11-16T10:00:00Z",
  "manage_data": { "name": "veristar", "value": "<32 hex>" },
  "expires_at": "2026-11-16T10:00:00Z"
}
```

**`POST /api/tenants/{tenant_id}/onchain/ids/{chain_id}/verify`**: any member,
terms accepted, not `suspended`, at most 10 attempts per id and hour (429
`rate_limited`). Body by method:

| Method | Kind | Body | The proof holds when |
|---|---|---|---|
| `sep53` | account | `{"method": "sep53", "signature": "<base64 or hex>"}` | the signature is the account's ed25519 signature of SHA-256(`"Stellar Signed Message:\n"` + `message`), SEP-53, over the open challenge's message |
| `manage_data` | account | `{"method": "manage_data"}` | the account's data entry `veristar` on the ledger (Horizon) equals the open challenge's nonce |
| `contract_admin` | contract | `{"method": "contract_admin"}` | the contract's instance storage (RPC `getLedgerEntries`) holds an address under the key `Admin`, `admin`, `Owner` or `owner` (a symbol, or a one-element vector of one, as Rust enum variants such as OpenZeppelin's `Owner` are stored) that is a verified id of the same project |
| `contract_deployer` | contract | `{"method": "contract_deployer", "tx_hash": "<64 hex>"}` | the successful transaction (Horizon) creates the contract from an address, the contract id recomputed from network, address and salt equals the declared id, and the address is a verified id of the same project |

The answer is 200 with the id object in every case: a proof that does not
hold sets `verification.last_error`: `no_challenge`, `challenge_expired`,
`bad_signature`, `data_entry_mismatch`, `no_verified_owner`,
`deployer_not_verified`, `contract_not_created_in_transaction`,
`transaction_failed`, `no_admin_entry`, `admin_not_verified`, or a source code
of 15.3. A held proof clears the challenge, so a signature works once.
`verification.via` names the verified account a contract was proven through;
`detail` holds `storage_key` or `tx_hash`.

The proofs follow SEP-53 exactly, so any wallet that signs messages with it
works; the console asks through the Stellar Wallets Kit. A wallet that cannot
sign messages, or an account behind a multisig policy, uses the manage-data
method, which any wallet or the Stellar Lab can submit.

### 15.5 Two-way stellar.toml link

`link.sites` has one entry per site that is not removed:

```json
{
  "site_id": "uuid", "domain": "example.org", "verified": true,
  "stellar_toml": "found", "org_url": "https://example.org", "org_url_matches": true,
  "accounts": [ { "strkey": "G...", "listed_in_toml": true, "home_domain": "example.org",
                  "home_domain_matches": true, "two_way": true } ],
  "contracts": [ { "strkey": "C...", "listed_in_toml": false } ],
  "two_way": true
}
```

The link uses SEP-1 fields only and adds no key:

- Domain to ledger: the stellar.toml served at the verified domain (5.12) lists
  the account under `ACCOUNTS`, and its `[DOCUMENTATION] ORG_URL` is on that
  domain (`org_url_matches`).
- Ledger to domain: the account's `home_domain`, which only the account's
  signers can set with a SetOptions operation, equals the domain.

An account is `two_way` when the domain is verified (5.8) and every leg
holds; a site and the project are `two_way` when any account is. A contract
listed as a `[[CURRENCIES]]` `contract` is `listed_in_toml`; contracts have no
home domain and are not part of `two_way`.

### 15.6 The jobs task `chain-activity`

Hourly, under the jobs lock. It reads every id of every tenant that is not
removed whose newest stored day is older than yesterday, the longest unread
first, at most 200 per run, and an id whose last read failed again 50
minutes after the failure. An id declared by several projects is read once
per run. Each id's days are written in one transaction under the tenant's
shared advisory lock of 00019, with its read state; nothing is written for a
removed tenant. Requests are paced per source and retried on 429 and 5xx with
exponential backoff and jitter, never shorter than a `Retry-After`; after six
attempts the id records `rate_limited` or `source_unavailable` and the run is
marked failed in the scheduler metrics. Metric:
`jobs_chain_reads_total{kind, result}`.

### 15.7 Operators, the machine API and MCP

- `GET /api/operator/web/projects` and `GET /api/operator/api/projects`: every
  row carries `onchain` (**Summary**: `ids`, `ids_verified`, `operations`,
  `transactions` over the range, `days_missing` (id-days of the range before
  today without a row), `ids_failing` (ids whose last read failed) and
  `weekly_active_accounts` (15.8)). The column is not a sort key.
- `GET /api/operator/web/compare` and `GET /api/operator/api/compare`: every
  project carries `onchain`: `totals` (Summary), `series`, one point per day
  of the range: `ids_read` and the summed `operations` and `transactions`,
  both `null` on a day no id has a row for, and `weeks`, the weeks of 15.8
  that lie whole inside the range.
- `GET /api/operator/tenants/{tenant_id}/onchain?from=&to=`: the view of 15.3
  for one project, read as `cp_operator` over `cp.operator_sites`; 404 for an
  unknown or internal tenant.
- Machine API (section 9, `docs/reference/openapi/metrics-v1.yaml`):
  `GET /api/metrics/v1/onchain/ids` (declared ids with proof and read state,
  keyset by tenant and strkey) and `GET /api/metrics/v1/onchain/daily`
  (stored daily rows of declared ids, keyset by tenant, strkey and day) and
  `GET /api/metrics/v1/onchain/weekly` (one item per tenant and ISO week whose
  Monday lies in the range: `tenant_id`, `week`, `from`, `to`,
  `active_accounts`, `missing`, `closed_at`; keyset by tenant and week), with
  the `tenant`, `from`, `to`, `limit` and `cursor` rules of section 9.
- MCP (section 12): tenant tools `get_onchain_activity` (15.3) and
  `get_scf_view` (16.3), operator tool `get_tenant_onchain`; all carry the
  weekly active accounts.

### 15.8 Weekly active accounts

The SCF Growth Hack official rules define an active account as "the account
that initiated at least one operation" in a week
([SCF handbook, Growth Hack official rules, Top 5 Projects Selection](https://stellar.gitbook.io/scf-handbook/supporting-programs/growth-hack/official-rules)).
For a project, `active_accounts` of an ISO week (Monday to Sunday, UTC) is the
number of distinct G accounts that initiated at least one successful
operation involving one of the project's **verified** ids that week:

| Kind | Initiators of a day |
|---|---|
| account | the `source_account` of each operation `GET /accounts/{id}/operations` returns; Horizon fills it with the transaction's source when the operation names none, and with the base G account of a muxed source |
| contract | for each operation with an event of the contract (15.2), the operation's source, or the transaction's when it names none, read from the envelope through `getTransaction` (JSON-RPC batches of 50); for a fee bump, the inner transaction's; a muxed source counts as its G account |

- The task `chain-activity` (15.6) reads a day's initiators together with its
  counts, once the day is over, so contract days are read while Stellar RPC
  still holds them. A day stored before its initiators were read is read
  again while its week is open.
- While a week is open, `agg.chain_weeks` holds a random 32-byte key for the
  project and week, and `agg.chain_week_members` holds
  HMAC-SHA256(key, account) per id and initiating account, so the distinct
  count spans days and ids without storing an address. When the week closes
  the task stores `active_accounts`, deletes the members and drops the key.
- A week closes on the first run after its Sunday at which every verified
  id has all seven days with their initiators; the count is over the members
  of the ids verified at that moment. It closes as missing at once when a
  day can never be read whole, and at the latest 7 days after its Sunday.
- `agg.chain_daily.initiators` records per day: `read`,
  `day_over_read_cap` (a contract day with more than 10,000 distinct
  transactions to look up; the day's counts stay) or `before_source_history`.
- A week without a count has `active_accounts` `null` and a `missing`
  reason, never 0: `week_not_complete` (the week is not over),
  `not_read_yet` (over, still reading), `no_verified_ids`,
  `day_over_read_cap`, `before_source_history` (an id's source started
  after the week's Monday, or a day is outside what the source holds),
  `week_not_counted` (the week ended before the task counted the project),
  or an id's last read error.
- **WeeklyActive** of a range: `average` (mean of the counted weeks that lie
  whole inside the range, `null` when none), `latest` (the last counted
  week), `weeks_counted` and `weeks_missing`.

## 16. Exports and "What SCF sees"

### 16.1 Operator exports

All under `/api/operator` (403 `operator_required` otherwise), `text/csv;
charset=utf-8` as an attachment, streamed, one header row:

| Route | Rows |
|---|---|
| `GET /web/projects.csv` | the rows of `GET /web/projects` for the same `from`, `to`, filters, `sort` and `order`, from the first row on, at most `rows` of them |
| `GET /api/projects.csv` | the same for `GET /api/projects` |
| `GET /web/compare.csv?tenants=&from=&to=` | one row per project and day of `GET /web/compare` |
| `GET /api/compare.csv?tenants=&from=&to=` | one row per project and day of `GET /api/compare` |

- `rows` is 1 to 5000, default 50; the console sends the number of rows it
  shows, with the range it resolved, so the file holds exactly the rows on
  screen. `limit` and `cursor` are 400 `invalid_query`.
- The list exports carry the cohort, change and percentile columns of 19.2:
  `cohort_program`, `cohort_round`, `cohort_category`, `cohort_award_date`,
  `cohort_size`, then `<metric>_value`, `<metric>_previous`,
  `<metric>_growth` (`error_rate_change` for the error rate) and
  `<metric>_percentile` for `visitors`, `pageviews`, `requests`,
  `error_rate` and `onchain_operations`. The compare exports carry
  `cohort_program`, `cohort_round`, `cohort_category`, `cohort_award_date`
  and `after_award` (the day is on or after the award date; empty without
  one) per row.
- `GET /rules/{rule_id}/matches.csv` (19.4) is one row per meeting project
  and month: the rule, the project, its cohort, its integrity grade over the
  window, the month, its qualifying days and the days with their values as a
  JSON array.
- The list exports read with the list's own SQL in one repeatable-read
  snapshot, and add the on-chain summary columns `onchain_ids`,
  `onchain_ids_verified`, `onchain_operations`, `onchain_transactions`,
  `onchain_days_missing`, `onchain_ids_failing`,
  `onchain_weekly_active_accounts_average`,
  `onchain_weekly_active_accounts_latest`, `onchain_weeks_counted` and
  `onchain_weeks_missing` (15.8). The compare exports add `onchain_ids_read`,
  `onchain_operations` and `onchain_transactions` per day, and the ISO week
  of the day with its count or reason (`onchain_week`,
  `onchain_week_active_accounts`, `onchain_week_missing`), empty for a day
  of a week that is not whole inside the range.
- A value that is not known (a ratio without a denominator, a day not read) is
  an empty cell, never 0. A text cell that a spreadsheet would read as a
  formula (starting with `=`, `+`, `-`, `@`, tab or carriage return) gets a
  leading apostrophe.
- The API exports end their numbers with an `infrastructure_requests` column
  (7.4).
- Every export writes one `export.csv` audit row (3.3) with the query it
  answered.

### 16.2 Tenant overview export

**`GET /api/tenants/{tenant_id}/overview.csv?from=&to=`**: any member, range
rules of 15.3. One row per UTC day, read under `cp_tenant`, so row-level
security keys every table on the caller's tenant:

`day, web_pageviews, web_visitors, web_visits, web_bounces,
web_totaltime_seconds, web_quarantine_pageviews, api_requests,
api_errors_4xx, api_errors_5xx, api_infrastructure_requests, onchain_ids_read, onchain_operations,
onchain_transactions, integrity_grade`

The web columns are the project's `agg.web_daily` rows, summed over its sites
(empty on a day not aggregated yet); the API columns are the daily series of
7.6 (0 on a day without requests, as on the API traffic page); the on-chain
columns are as in 16.1, and `integrity_grade` is the day's grade of 17. It
writes one `export.csv` audit row.

### 16.3 What SCF sees

**`GET /api/tenants/{tenant_id}/scf-view?from=&to=`**: any member. The range
rules and defaults are those of section 8, so the numbers equal what an
operator sees for the same range. The route reads with the operator views'
own SQL under `cp_operator`, for the tenant of the caller's verified
membership only; it takes no tenant list and never names another project.

```json
{
  "from": "2026-10-18", "to": "2026-11-16", "days": ["..."],
  "in_operator_views": true,
  "tenant": "TenantRef", "sites": { "sites": 1, "sites_verified": 1, "sites_with_stellar_toml": 1 },
  "has_web_data": true, "has_api_data": false,
  "web": "the project's row of 8.3 without tenant, site counts and top lists",
  "api": "the project's row of 8.5 without tenant and site counts",
  "onchain": "Summary of 15.7",
  "integrity": "the row's grade of 17.3",
  "link_two_way": true,
  "cohort": "CohortRecord of 19.1",
  "percentile_cohort": { "program": "Build Award", "round": "SCF #38" },
  "comparison": "cohort, cohort_size and kpis of 19.2",
  "rules_met": [{ "rule": "Rule of 19.4", "match": "RuleMatch of 19.4" }],
  "web_series": "the series of 8.4", "api_series": "the series of 8.6",
  "onchain_series": "the series of 15.7"
}
```

- `percentile_cohort` is the population of the percentiles: the projects
  confirmed in the project's program and round, or every listed project
  (both fields `null`) when it has no confirmed cohort. `comparison` is the
  project's own row of 19.2 computed in that population.
- `rules_met` holds every saved rule the project meets when the request is
  answered (19.4), with its evidence.

`in_operator_views` is false for an internal project, which the operator
views do not list; `tenant`, `sites`, `web`, `api`, `onchain` and
`integrity` are then `null` or absent and the series empty.

---

## 17. Integrity grade of web traffic

Every site gets an integrity grade per UTC day: `A`, `B` or `C`, or none for a
day with too little traffic, with the reasons that set it. The jobs task
`integrity` computes it hourly for today and the three days before from
platform-observed evidence and stores it in `agg.web_integrity_daily`. The
rules, their inputs and thresholds are in
[../security/bot-and-inflation.md](../security/bot-and-inflation.md) section 9.
The project reads its own rows under `cp_tenant` with row-level security,
operators read every project's under `cp_operator`; both get the same report.

### 17.1 Objects

**Report**, the answer of 17.2 and 17.3:

```json
{
  "tenant_id": "uuid", "from": "2026-11-01", "to": "2026-11-30", "rule_version": 2,
  "grade": "B", "graded_days": 30, "days_a": 22, "days_b": 6, "days_c": 2,
  "coverage": { "script_requests": 4200, "gpc_requests": 504, "unmeasured_share": 0.12 },
  "reasons": [
    { "code": "hosting_share", "level": "C", "days": 8,
      "text": "A large share of events came from hosting networks or anonymisers." }
  ],
  "sites": [
    {
      "site_id": "uuid",
      "grade": "B", "graded_days": 30, "days_a": 22, "days_b": 6, "days_c": 2, "reasons": ["RangeReason"],
      "coverage": "Coverage",
      "days": [
        {
          "day": "2026-11-30", "grade": "C", "closed": false, "rule_version": 2,
          "reasons": [
            { "code": "hosting_share", "level": "C", "value": 0.62, "limit": 0.5,
              "text": "62% of events came from hosting networks or anonymisers (limit 50%). ..." }
          ],
          "evidence": {
            "events": 1840, "quarantined": 120, "proof_failures": 35, "networks": 41,
            "visitors": 610, "pageviews": 1500, "baseline_visitors": 590, "baseline_days": 28,
            "net": { "access": 600, "organisation": 50, "hosting": 1100, "anonymiser": 40, "unknown": 50 },
            "hosting_share": 0.6195652173913043, "quarantine_share": 0.06521739130434782,
            "proof_pass_rate": 0.9813333333333333,
            "signal_views": 1400, "framed": 14, "never_visible": 60,
            "framed_share": 0.01, "never_visible_share": 0.0410958904109589
          },
          "coverage": { "script_requests": 140, "gpc_requests": 17, "unmeasured_share": 0.12142857142857143 }
        }
      ]
    }
  ]
}
```

- `grade` of a day is `null` when it had fewer than 50 accepted events; its
  only reason is `too_little_traffic` with level `none`.
- Reason codes: `hosting_share`, `quarantine_share`, `few_networks`, `spike`,
  `framed_share` and `never_visible_share` (rule version 2), `proof_failures`,
  `page_volume`, `too_little_traffic`. `rule_version` of a day is the version
  that graded it; the report's `rule_version` is the version the jobs apply
  now. `level` is the grade
  the reason caps the day at (`B`, `C`) or `none`. `value` and `limit` are the
  observed figure and the threshold it crossed (a share from 0 to 1, a count
  or a factor). `text` is the plain language form; the same text reaches the
  project and operators.
- A range grade (`grade` of a site or of the report) combines the graded site
  days: `C` when at least a quarter of them are `C`, `B` when at least a
  quarter are `B` or `C`, `A` otherwise, `null` without a graded day. Range
  reasons count the days each code applied to, with the worst level.
- `closed` is `true` once the UTC day has ended. A day is graded again while
  the web aggregates of 8 may still change it (three days).
- `evidence.net` counts accepted events per network class; `networks` is the
  number of distinct autonomous systems. Neither holds an address or a
  network number.
- `evidence.signal_views`, `framed` and `never_visible` are the page signals
  of tracker 1.3.0 (page views of releases that report them, the framed part,
  never-visible notices); `framed_share` and `never_visible_share` are their
  shares, `null` without a report. Days graded before rule version 2 read 0.
- `coverage` (17.5) is the estimate of visits not measured because of privacy
  signals, for the report's range, each site's range and each day.

### 17.2 `GET /api/tenants/{tenant_id}/integrity`

Member (1.3). Query `from`, `to`: UTC days, both included, `to` not after
today, at most 366 days; without `to` the range ends today, without `from`
it holds 30 days. **200** Report of the tenant. 400 `invalid_query` for a bad
or unknown parameter. A project without graded days gets a report with
`grade` `null` and no sites.

### 17.3 `GET /api/operator/web/integrity`

Operator. Query `tenant` (required, lowercase UUID), `from`, `to` (8.1).
**200** Report of that project. 400 `invalid_query` without `tenant` or for
an unknown parameter; 404 `not_found` for an unknown or internal tenant.

The project list of 8.3 carries the range grade of each row as
`integrity` (`grade`, `graded_days`, `days_a`, `days_b`, `days_c`) and sorts
by it with `sort=integrity` (A first ascending; projects without a graded day
last in both orders). Each project of 8.4 carries `integrity` with the range
grade and its reasons.

### 17.4 Machine API and exports

The coverage object of 17.5 travels with the grade in each item below.

- `GET /api/metrics/v1/web/integrity` (9.3 rules): parameters `tenant`,
  `from`, `to`, `integrity_at_or_below`, `limit`, `cursor`. One item per
  tenant, site and day of `agg.web_integrity_daily`: `tenant_id`, `site_id`
  and the day object of 17.1. Ordered by tenant, site and day.
  `integrity_at_or_below` (`A`, `B` or `C`) keeps the site days graded that
  grade or worse; a day without a grade never matches.
- Every `GET /api/metrics/v1/web/daily` item carries `integrity`: the range
  grade of the tenant's sites on that day, the same on both bands' items.
- The CSV exports carry the grade: `integrity_grade`, `integrity_days_a`,
  `integrity_days_b` and `integrity_days_c` per row of the web project
  export; `integrity_grade` per project and day of the web compare export and
  per day of the project overview export.
- MCP: the project tool `get_web_integrity` reads 17.2, the operator tool
  `get_tenant_integrity` reads 17.3; `get_global_web_overview` accepts
  `sort` `integrity` and the filter `integrity_at_or_below`.
- The web project list (8.2), its CSV export and the MCP overview filter on
  the range grade with `integrity_at_or_below`.

### 17.5 Appeals

The grade is computed by the rules of 17.1 and stored per site and day; no
route sets or edits it. The project overview and "What SCF sees" link to a
new issue on the **Integrity appeal** form of the public tracker
(`.github/ISSUE_TEMPLATE/integrity-appeal.yml`), with the fields `project`
(slug), `period`, `grade`, `sites` (domain and grade per site), `reasons`
(code, level and day count) and `rule_version` filled in from 17.2, and no
personal or visitor data. Operators answer and decide on the issue; the
stored grade does not change.

### 17.5 Coverage: visits not measured because of privacy signals

Visitors whose browser sends Global Privacy Control are not measured, by
design. The gateway counts, per site and UTC day, the requests for the tracker
file a browser made to run it and the part that carried `Sec-GPC: 1`,
attributed to the site by the page origin of the `Referer` header
([../compliance/privacy-strategy.md](../compliance/privacy-strategy.md)
section 6, with the method and its accuracy limits). The jobs task
`edgestats` stores them in `agg.web_coverage_daily`, under row-level security
like the grades.

**Coverage**:

```json
{ "script_requests": 4200, "gpc_requests": 504, "unmeasured_share": 0.12 }
```

`unmeasured_share` is `gpc_requests / script_requests`, `null` below 100
requests. Where it appears:

- the Report of 17.1 (range, site and day), so 17.2 and 17.3, the project's
  overview, the operator's project detail and the MCP tools of 17.4;
- each row of 8.3 as `coverage`, sortable with `sort=unmeasured_share`
  (projects without a share last in both orders), and each project of 8.4;
- `web.coverage` of what SCF sees (16.3);
- each `GET /api/metrics/v1/web/daily` item (the tenant's sites on that day)
  and each `GET /api/metrics/v1/web/integrity` item (that site and day);
- the CSV exports: `script_requests`, `gpc_requests` and `unmeasured_share`
  per row of the web project export, per project and day of the web compare
  export, and per day of the project overview export.

---

## 19. Cohorts, growth, percentiles and threshold rules

SCF decides by program, round, category and award date. SCF publishes no
machine-readable directory of its awards, so a project proposes its cohort
and an operator confirms it; only confirmed values place a project in a
cohort. The values live in `cp.tenant_cohorts` (migration 00035): the
project writes only the proposal columns under `cp_tenant`, operators write
the confirmed columns under `cp_system`, operators read under `cp_operator`.

### 19.1 Cohort routes

**CohortRecord**:

```json
{
  "tenant_id": "uuid",
  "confirmed": { "program": "Build Award", "round": "SCF #38", "category": "DeFi", "award_date": "2026-10-01" },
  "confirmed_at": "2026-11-16T09:00:00.000Z",
  "proposal": null,
  "proposed_at": null
}
```

`confirmed` and `proposal` are `null` when not set; inside them any field
may be `null`. Program, round and category are trimmed text of 1 to 80
characters without control characters; `award_date` is a UTC day from
2018-01-01 to today.

| Route | Who | Does |
|---|---|---|
| `GET /api/tenants/{tenant_id}/cohort` | member | the project's CohortRecord |
| `PUT /api/tenants/{tenant_id}/cohort/proposal` | owner | body: a cohort object with at least one field; replaces the proposal, audit `cohort.propose`; **200** CohortRecord |
| `GET /api/operator/cohorts` | operator | the confirmed values in use with their project counts (`programs`, `rounds`, `categories`: `[{"value", "projects"}]`) and every pending proposal (`proposals`: `[{"tenant", "confirmed", "proposal", "proposed_at"}]`), removed and internal projects left out |
| `GET /api/operator/tenants/{tenant_id}/cohort` | operator | the CohortRecord |
| `PUT /api/operator/tenants/{tenant_id}/cohort` | operator | body: a cohort object; sets the confirmed values, an object of nulls clears them; audit `cohort.set`; **200** CohortRecord |
| `POST /api/operator/tenants/{tenant_id}/cohort/confirm` | operator | copies the proposal into the confirmed values and clears it; audit `cohort.confirm`; 409 `no_proposal` without one |
| `DELETE /api/operator/tenants/{tenant_id}/cohort/proposal` | operator | clears the proposal, leaves the confirmed values; audit `cohort.proposal_dismiss`; 409 `no_proposal` without one |

A project also proposes its cohort at enrollment (4.2.1). Operator routes
answer 404 `not_found` for an unknown or internal project, 422 for an invalid
value.

### 19.2 Growth and percentiles in the global views

Every row of 8.3 and 8.5 carries:

```json
{
  "cohort": { "program": "Build Award", "round": "SCF #38", "category": "DeFi", "award_date": "2026-10-01" },
  "cohort_size": 14,
  "kpis": {
    "visitors":           { "value": 1200, "previous": 900, "change": 0.3333, "percentile": 0.7857 },
    "pageviews":          { "value": 3100, "previous": 2500, "change": 0.24, "percentile": 0.7142 },
    "requests":           { "value": 0, "previous": 0, "change": null, "percentile": null },
    "error_rate":         { "value": null, "previous": null, "change": null, "percentile": null },
    "onchain_operations": { "value": 52, "previous": 13, "change": 3, "percentile": 0.5 }
  }
}
```

- `cohort` is the confirmed cohort, every field `null` without one.
- The five key metrics: `visitors` (primary-band visitors, the verified
  count of 8.3), `pageviews` (primary band), `requests` and `error_rate`
  (requests answered 500 to 599 over requests) with the source rule of 8.5,
  and `onchain_operations` (15.7). `value` is the range, `previous` the range
  of equal length that ends the day before `from`.
- `change` is `(value - previous) / previous` for the four counts and
  `value - previous` for the error rate; `null` when `previous` is 0 or
  missing.
- The percentile **population** is every listed project that is `active` or
  `suspended` and matches the cohort filters (8.2) of the request, whatever
  its other filters: a name search never moves a percentile. `cohort_size`
  is the size of that population. A project outside it (pending, rejected,
  removed) has metrics and percentiles of `null`.
- `percentile` is the share of the population, among the projects with a
  value, whose value is at or below this project's (SQL `cume_dist` over
  the aggregates): from just above 0 to 1, where 1 is the largest value. A
  count of 0 and an error rate without requests have no value and no
  percentile. For the error rate, a higher percentile is a higher error
  rate.
- The compare projects of 8.4 and 8.6 carry `cohort`; the console draws the
  award date on their charts.

### 19.3 What a project sees

"What SCF sees" (16.3) shows the project its CohortRecord, its row of 19.2
computed in the population of its own program and round (or every listed
project without a confirmed cohort), and the rules it meets.

### 19.4 Threshold rules

**Rule**:

```json
{
  "id": "uuid", "name": "Steady web traction", "metric": "visitors",
  "min_per_day": 100, "min_days": 10, "months": 3, "min_grade": "B",
  "cohort": { "program": "Build Award", "round": null, "category": null },
  "created_at": "...", "updated_at": "..."
}
```

A rule reads: in each of the last `months` complete UTC calendar months, at
least `min_days` days with `metric` at or above `min_per_day`, and, when
`min_grade` is set, an integrity grade (17) over those months of at least
`min_grade` (A is best). `metric` is `visitors` (primary band, summed over
the project's sites per day), `requests` (the source rule of 8.5 over the
window) or `onchain_operations`. Limits: `min_per_day` 1 to 10^12,
`min_days` 1 to 28, `months` 1 to 12, at most 200 rules. `cohort` limits the
evaluation to projects confirmed in it; its fields are optional.

| Route | Does |
|---|---|
| `GET /api/operator/rules` | `{"items": [Rule]}`, oldest first |
| `POST /api/operator/rules` | body: the rule's fields; audit `rule.create`; **201** Rule; 409 `rule_limit` beyond 200 rules |
| `GET /api/operator/rules/{rule_id}` | the Rule |
| `PUT /api/operator/rules/{rule_id}` | replaces every field; audit `rule.update` with old and new values; **200** Rule |
| `DELETE /api/operator/rules/{rule_id}` | audit `rule.delete`; **204** |
| `GET /api/operator/rules/{rule_id}/matches?as_of=` | the projects that meet it, with the evidence |
| `GET /api/operator/rules/{rule_id}/matches.csv?as_of=` | the same as CSV (16.1) |

`as_of` is a UTC day not after today, default today; the window is the
`months` complete calendar months before its month. Every `active` or
`suspended` listed project in the rule's cohort is evaluated:

```json
{
  "rule": "Rule", "as_of": "2026-11-16", "from": "2026-08-01", "to": "2026-10-31",
  "months": ["2026-08", "2026-09", "2026-10"], "evaluated": 40,
  "items": [
    {
      "tenant": "TenantRef", "cohort": "Cohort of 19.2", "meets": true,
      "integrity": { "grade": "A", "graded_days": 90, "days_a": 88, "days_b": 2, "days_c": 0 },
      "months": [ { "month": "2026-08", "qualifying_days": 12, "days": [ { "day": "2026-08-03", "value": 140 } ] } ]
    }
  ]
}
```

`items` holds the projects that meet the rule, ordered by name; `days` lists
each qualifying day with the project's value. The machine API serves the
same reads at `GET /api/metrics/v1/rules` and
`GET /api/metrics/v1/rules/{rule_id}/matches` (9.3 rules; a key scoped to one
tenant evaluates that tenant only), and MCP at `list_threshold_rules` and
`get_threshold_rule_matches`.

---

## 20. Signed usage reports

A member generates a report of the project for a period of closed UTC days.
The control plane reads the same aggregates as "What SCF sees" (16.3),
writes the report in its canonical JSON form, signs those bytes with the
platform's report key with SEP-53 and stores both in `cp.usage_reports`
(migration 00036). A stored report never changes: a trigger refuses any
change of its body, signature, signer, period or author for every role. The
format, the canonical form and offline verification are in
[usage-reports.md](usage-reports.md).

### 20.1 Member routes

| Route | Who | Does |
|---|---|---|
| `POST /api/tenants/{tenant_id}/reports` | member, current terms | body `{"from", "to"}`; builds, signs and stores a report; audit `report.create`; **201** Detail |
| `GET /api/tenants/{tenant_id}/reports` | member | `{"items": [Summary]}`, newest first, at most 200 |
| `GET /api/tenants/{tenant_id}/reports/{report_id}` | member | Detail, revoked reports included |
| `POST /api/tenants/{tenant_id}/reports/{report_id}/revoke` | member | revokes the shared link; audit `report.revoke`; a second revoke answers the same Summary and writes nothing; **200** Summary |

**Summary**: `id`, `url` (`https://app.<BASE_DOMAIN>/r/<id>`), `from`, `to`,
`signer`, `created_at`, `created_by`, `revoked_at`, `view_count`,
`last_viewed_at`. **Detail** adds `file`, the signed file of 20.3.

- The period is UTC days, both included, `to` before today (a signed number
  never grows after signing), at most 366 days: 422 otherwise.
- 409 `not_reportable` for a project the operator views do not list.
- Limits: 20 reports per project per 24 hours and 60 revocations per project
  per hour, 429 `rate_limited` with `Retry-After` beyond them.
- The id is 128 random bits in lowercase base32 (26 characters); the link is
  the only way to it from outside the project.

### 20.2 The report key

The report key is an Ed25519 Stellar key (`REPORT_SIGNING_SEED`, 4.5). Its
account id is published in three places that must agree:

- `GET https://app.<BASE_DOMAIN>/.well-known/stellar.toml`: SEP-1 `ACCOUNTS`
  lists the current key first, then every retired key. The control plane
  writes the file from the key it signs with, with
  `Access-Control-Allow-Origin: *` and `text/plain`.
- `GET /api/public/report-keys`: `{"current", "retired", "stellar_toml"}`.
- The public status page `status.<BASE_DOMAIN>`, in its description and
  a "Report signing key" link to the stellar.toml.

`SIGNING_KEY` of SEP-1 is not used: SEP-1 reserves it for SEP-10 and SEP-45
authentication servers. The key holds no funds and signs no transaction.
Rotation is in the secret rotation runbook.

### 20.3 Public route

**`GET /api/public/reports/{report_id}`**: no session.

```json
{
  "id": "…", "url": "https://app.<BASE_DOMAIN>/r/…", "created_at": "…",
  "file": { "report": { "format": "veristar-usage-report", "…": "…" },
            "signature": { "scheme": "sep-53", "signer": "G…", "value": "base64" } },
  "keys": { "current": "G…", "retired": [], "stellar_toml": "https://app.<BASE_DOMAIN>/.well-known/stellar.toml" }
}
```

- The removal purge of 5.5 deletes a removed project's reports in its last
  step, `reports` (through `cp.purge_usage_reports`, which refuses a tenant
  that is not removed); its links answer 404 from the moment of removal.
- It reads exactly one row by id under `cp_system`. An unknown or malformed
  id, a revoked link and a removed project all answer the same 404
  `not_found`.
- Every answer increments `view_count` and writes a `report.view` audit row
  without any trait of the reader.
- Limits: 300 views of one report per hour, and 20 requests per second
  (burst 60) per control plane instance whatever the id; 429 `rate_limited`
  beyond them.
- The console page `/r/<id>` shows the report, checks the signature in the
  browser against the published keys and offers the signed file as JSON.

### 20.4 Isolation

A report link answers its own report alone: the route takes no tenant, reads
one row by its unguessable id and returns that row's body, which holds the
aggregates of one project only. The member routes follow 1.3: a member of
another project gets 404 for every report id. The isolation gate
(`tests/isolation/console`) covers every route of this section.


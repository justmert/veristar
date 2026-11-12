# Control plane HTTP API

The contract between the console (`apps/console`) and the control plane
(`cmd/controlplane`, `internal/controlplane`). Both sides build against this file.
A change to a route, a field or an error code is made here first, in the same
commit as the code.

- Plan reference: `.scratch/plan.md` sections 4.7 to 4.10 and this stage.
- Database names: tables and columns are those of `db/migrations/00002_cp_tables.sql`
  and `00003_cp_security.sql`. Where a response field maps to a column, the field
  has the column's name.
- Research behind the choices: `docs/reference/the reference docs`.

Sections 1 to 3 hold the conventions every phase follows. Section 4 onward holds
one section per phase. Later phases append a section (`## 5. this stage: ...`,
`## 6. this stage: ...` and so on), add their error codes to section 2.4 and their
audit actions to section 3.3, and never change the meaning of an existing field.

---

## 1. Transport

| Item | Rule |
|---|---|
| Base URL | `https://app.<BASE_DOMAIN>/api`. Caddy (`deploy/caddy/app.Caddyfile`) serves the console and proxies `/api/*` to the control plane unchanged. |
| Dev base URL | `http://localhost:8085/api` (callback of an operator input: `http://localhost:8085/api/auth/github/callback`). 8080 is the dev gateway, so the control plane listens on 8085 and is also the dev console origin. |
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
  (`cp.sessions.user_id`). See the reference docs section 3 for why `pgxstore` is not used.
- The token is renewed (`RenewToken`) at sign-in, so a session id fixed before
  sign-in is useless afterwards.
- **OAuth state cookie.** `sa_oauth`, only during sign-in: `HttpOnly`, `Secure`,
  `SameSite=Lax`, `Path=/api/auth/github/callback`, `Max-Age=600`. Value: AES-256-GCM
  sealed (key `OAUTH_STATE_KEY`) JSON `{state, verifier, return_to, exp}`. No database
  row is written for a visitor who has not signed in.
- A route marked **session** answers 401 `unauthenticated` without a valid session.
  A route marked **public** needs none.

### 1.2 Cross-site request forgery

Every request with method `POST`, `PUT`, `PATCH` or `DELETE` must carry both:

1. `X-Requested-With: sa-console` (exact, case-sensitive value). Missing or different:
   403 `csrf_header_missing`. A cross-site form cannot set a custom header, and a
   cross-site `fetch` with one needs a CORS preflight, which the control plane never
   grants (it sends no `Access-Control-Allow-*` header on any route).
2. `Origin` equal to `CONSOLE_ORIGIN` (`https://app.<BASE_DOMAIN>`; dev
   `http://localhost:8085`). Missing or different: 403 `origin_mismatch`.

`GET` and `HEAD` never change state. The only `GET` routes with side effects outside
the database are the two browser redirects (`/api/auth/github/start` and
`/api/sso/umami`), covered in their sections.

### 1.3 Tenant scoping

- Every route under `/api/tenants/{tenant_id}` and `/api/sites/{site_id}` first checks
  membership under `cp_system` (`cp.tenant_members` for the session user). A
  non-member gets **404 `not_found`**, never 403, for every such route, also for ids
  that exist. The isolation tests (the design) assert this.
- The handler then runs under `cp_tenant` through `db.WithTenant(ctx, tenant_id, fn)`.
- `/api/sites/{site_id}` resolves the site's `tenant_id` under `cp_system`, then
  applies the same rule.
- A member whose role is not enough for the action gets **403 `owner_required`**.
  Members already know the tenant exists, so 403 reveals nothing.
- Tenants with `cp.tenants.status = 'removed'` answer 404 for every member (this stage
  sets that status; the rule is listed here so this stage code already honours it).

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
- `message`: English text for people. May change.
- `fields`: present only for 422; maps a JSON path (`name`, `origins[1]`,
  `enrollment.stellar_ids[0]`) to a field code from 2.5.
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
| 401 | `unauthenticated` | no session, expired session |
| 403 | `csrf_header_missing` | see 1.2 |
| 403 | `origin_mismatch` | see 1.2 |
| 403 | `owner_required` | the action needs role `owner` |
| 403 | `terms_not_accepted` | the tenant has not accepted the current terms version (4.2.6) |
| 403 | `operator_required` | an `/api/operator` route without operator rights (5.1) |
| 404 | `not_found` | unknown id, malformed id, or not a member |
| 405 | `method_not_allowed` | method not defined for the path |
| 409 | `slug_taken` | `cp.tenants.slug` already exists |
| 409 | `already_member` | invite or add for a user who is a member |
| 409 | `invite_exists` | an open invite for that login exists (`invites_tenant_login_idx`) |
| 409 | `last_owner` | removing or demoting the last owner |
| 409 | `site_removed` | change to a site with `status = 'removed'` |
| 409 | `terms_version_mismatch` | accepted version is not the current one |
| 409 | `invalid_transition` | lifecycle action not allowed from the current status (5.5) |
| 409 | `tenant_suspended` | site create or update while the tenant is `suspended` (5.11); OTLP token create while `suspended` (6.2) |
| 409 | `tenant_not_active` | OTLP token create while the tenant is `pending` or `rejected` (6.2) |
| 410 | `tracker_version_revoked` | snippet for a revoked tracker version |
| 413 | `body_too_large` | body over 64 KiB |
| 415 | `unsupported_media_type` | see section 1 |
| 422 | `validation_failed` | field rules, details in `fields` |
| 429 | `rate_limited` | per-session or per-route limit; `Retry-After` header set |
| 500 | `internal` | anything unexpected; details only in the log |
| 503 | `unavailable` | database, NATS or Umami unreachable; `Retry-After` set |
| 503 | `provisioning_pending` | SSO before the reconciler created the Umami objects; `Retry-After` set |

### 2.5 Field codes (422)

`required`, `too_long`, `too_short`, `too_many`, `invalid_format`, `not_https_url`,
`not_bare_origin`, `origin_outside_domain`, `invalid_domain`, `invalid_strkey`,
`invalid_github_login`, `invalid_role`, `duplicate`, `unknown_tracker_version`.
this stage adds `invalid_value` (a value outside an enumerated set, such as
`mode`).

---

## 3. Shared objects

### 3.1 Objects

**User** (`cp.users`):

```json
{ "id": "uuid", "github_id": 1234567, "github_login": "octocat", "is_operator": false }
```

`is_operator` is set at every sign-in from `OPERATOR_GITHUB_IDS` (4.5): true when the
user's numeric GitHub id is listed, false otherwise, so removing an id takes effect
at that person's next sign-in. this stage builds the operator routes on it.

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
{ "user_id": "uuid", "github_id": 1234567, "github_login": "octocat", "role": "member", "created_at": "..." }
```

**Invite** (`cp.invites`):

```json
{ "id": "uuid", "github_login": "someone", "role": "member", "created_by": "uuid", "created_at": "...", "expires_at": "..." }
```

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
  "created_at": "...",
  "updated_at": "..."
}
```

- `status`: `pending`, `active`, `suspended`, `removed` (column check).
- `verify_token`, `last_verify_*` and `stellar_toml*` are this stage fields. this stage
  adds them to this object; `verify_token` is shown only through the this stage
  verification route.

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

this stage actions: `user.sign_in`, `user.sign_out`, `tenant.create`, `tenant.update`,
`tenant.terms_accept`, `member.add`, `member.role_change`, `member.remove`,
`invite.create`, `invite.revoke`, `invite.resolve`, `site.create`, `site.update`,
`site.delete`, `provision.team_create`, `provision.website_create`,
`provision.website_update`, `provision.website_delete`, `provision.user_create`,
`provision.user_update`, `provision.membership_add`, `provision.membership_remove`,
`provision.registry_put`, `provision.registry_delete`.
`umami-rotate` writes one `umami.password_rotate` row per run (actor
`system:umami-rotate`, `target` empty, detail: day and user count), not one per user.

this stage actions (section 5): `user.operator_sync`, `settings.enrollment_mode`,
`settings.removal_keep_aggregates`, `tenant.approve`, `tenant.reject`,
`tenant.resubmit`, `tenant.suspend`, `tenant.resume`, `tenant.remove`,
`site.review_queue`, `site.approve`, `site.reject`, `purge.step`,
`purge.complete`, `site.verify`, `site.stellar_toml`. Actors added:
`system:startup` (operator sync), `system:verify-domains`, `system:purge`.
Every this stage operator action carries `reason` in `detail` when one was given,
and every row about one tenant carries `detail.tenant_id`, which the audit
filter of 5.9 uses.

this stage actions (section 6): `otlp_token.create`, `otlp_token.revoke`,
`provision.otlp_registry_put`. Target added: `otlp_token:<id>`.

this stage (section 7) is read-only and adds no audit action.

---

## 4. this stage: sign-in, tenants, members, sites, snippet, ingest, SSO

### 4.1 Auth

#### 4.1.1 `GET /api/auth/github/start`

Public. Browser navigation, not `fetch`.

Query: `return_to` (optional): a console path starting with `/` and not with `//`
or `/\`, at most 512 characters. Anything else is replaced with `/`.

Effect: creates `state` (32 random bytes, base64url) and a PKCE verifier
(`oauth2.GenerateVerifier`), sets the `sa_oauth` cookie (1.1), and answers
**302** to `https://github.com/login/oauth/authorize` with `client_id`,
`redirect_uri` (`GITHUB_REDIRECT_URL`), `state`, `code_challenge`,
`code_challenge_method=S256` and `allow_signup=true`. No `scope` parameter is sent
(the reference docs section 1.3). Rate limited inside the control plane (core Caddy has
no rate limiter): one in-memory token bucket per control plane instance, shared by
all clients, 10 per second with a burst of 30. Over the limit: 429 `rate_limited`
with `Retry-After` (JSON envelope, no redirect, no `sa_oauth` cookie).

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
4. Exchange `code` with the cookie's verifier (`oauth2.VerifierOption`). Failure:
   `exchange_failed`.
5. `GET https://api.github.com/user` with the access token, headers
   `Accept: application/vnd.github+json` and `X-GitHub-Api-Version: 2022-11-28`.
   Use `id` (int64) and `login` only; the token is then discarded, never stored.
   Failure: `github_unavailable`.
6. Under `cp_system`, in one transaction: upsert `cp.users` (`UpsertUser`: key
   `github_id`, refresh `github_login` and `last_login_at`); resolve open, unexpired
   `cp.invites` whose `lower(github_login)` equals the login into `cp.tenant_members`
   and delete those invites (audit `invite.resolve`); audit `user.sign_in`.
7. Renew the session token, store `user_id`, answer **303** to `return_to`.

Failure: **303** to `/sign-in?error=<code>` with `code` one of `state_mismatch`,
`access_denied`, `github_error`, `exchange_failed`, `github_unavailable`,
`internal`. The console shows a message per code. No JSON body.

#### 4.1.3 `POST /api/auth/logout`

Session, CSRF rules apply. Body: none. Destroys the session row and expires the
cookie. Audit `user.sign_out`. **204**. Without a session: also 204 (idempotent).

#### 4.1.4 `GET /api/me`

Session.

```json
{
  "user": { "id": "uuid", "github_id": 1234567, "github_login": "octocat", "is_operator": false },
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
  "accepted_terms_version": "2026-11-12-draft.1"
}
```

- `slug` and `description` optional, `enrollment.stellar_ids` and
  `enrollment.scf_round_url` optional; the rest required (3.2).
- `accepted_terms_version` must equal the current version: else 409
  `terms_version_mismatch`.
- Under `cp_system`, one transaction: insert `cp.tenants` (`enrollment_answers` =
  the `enrollment` object, `accepted_terms_version`, `created_by`, `status` from
  `cp.settings` `enrollment_mode`: `open` gives `active`; `approval`, added in
  this stage, gives `pending`), insert the creator into `cp.tenant_members` with role
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
`enrollment_answers` is a this stage route (resubmission after rejection).
**200** Tenant. Audit `tenant.update`.

#### 4.2.5 Members and invites

`GET /api/tenants/{tenant_id}/members`: session, member.

```json
{ "members": [Member, ...], "invites": [Invite, ...] }
```

Members ordered by `github_login`, invites by `created_at`. Expired invites are
not listed. Not paginated (bounded by the limits below).

`POST /api/tenants/{tenant_id}/members`: session, owner.

```json
{ "github_login": "someone", "role": "member" }
```

- If a `cp.users` row with that login (case-insensitive) exists, the user is added
  to `cp.tenant_members` at once: **201** `{"kind": "member", "member": Member}`,
  audit `member.add`.
- Otherwise an invite is created with `expires_at = now() + 14 days`: **201**
  `{"kind": "invite", "invite": Invite}`, audit `invite.create`. It is resolved at
  that person's next sign-in (4.1.2 step 6).
- The login is not looked up on GitHub; a typo stays an unresolved invite until
  it expires or is revoked.
- Errors: 409 `already_member`, 409 `invite_exists`, 422, and 422 `too_many` on field
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
working. This is how this stage's new version is accepted at the next sign-in.

### 4.3 Sites

#### 4.3.1 `POST /api/tenants/{tenant_id}/sites`

Session, member.

```json
{ "name": "Marketing site", "domain": "example.org", "origins": ["https://example.org"] }
```

- The control plane makes `id` and `quarantine_website_id` (UUIDv7 each) and
  `verify_token` (32 random bytes, base64url, used from this stage).
- `status` is `active` when the tenant is `active`, else `pending`.
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

Session, member. Body: any of `name`, `origins` (whole list replaced). `domain` is
immutable (422 `invalid_format` on `domain`). **200** Site. 409 `site_removed`.
Audit `site.update`. Reconcile republishes the registry entry, so the gateway
applies new origins within its watcher delay.

#### 4.3.5 `DELETE /api/tenants/{tenant_id}/sites/{site_id}`

Session, owner. Sets `status = 'removed'` (the row stays: `cp.erasure_requests`
references it and this stage keeps removed domains). **202** Site with
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
- `npm`: `null` until the tracker package is published (this stage, an operator input. Then
  `{"package": "<name>", "version": "1.0.0", "install": "pnpm add <name>@1.0.0",
  "import": ...}`. The `import` line is **not yet defined**: this stage defines it with
  the published package. Until then the control plane refuses to start when
  `TRACKER_NPM_PACKAGE` is set, rather than answer with a guessed import. The console
  hides the npm tab while `npm` is `null`.
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
- The site must not be `removed`; the tenant's Umami user and team membership must
  exist (reconciler done). Otherwise failure `provisioning_pending`.
- Logs in to Umami on the private network (`POST /api/auth/login`) as `gh-<github_id>`
  with that user's derived password for the current UTC day. It never
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
Caddy serves on `web.<BASE_DOMAIN>` carries it too (the reference docs section 5).

### 4.5 Configuration keys (control plane, this stage)

Loaded by `internal/config`; a missing required key stops the process and names
the key.

| Key | Required | Meaning |
|---|---|---|
| `CONTROLPLANE_ADDR` | yes | API listener, for example `0.0.0.0:8085`; Caddy reaches it as `CONTROLPLANE_UPSTREAM` |
| `CONTROLPLANE_OPS_ADDR` | yes | `/healthz`, `/readyz`, `/metrics` |
| `CONSOLE_ORIGIN` | yes | exact `Origin` accepted on state changes (1.2) |
| `INGEST_ORIGIN` | yes | `https://in.<BASE_DOMAIN>` for snippets |
| `WEB_ORIGIN` | yes | `https://web.<BASE_DOMAIN>` for SSO redirects |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | yes | an operator input |
| `GITHUB_REDIRECT_URL` | yes | `<CONSOLE_ORIGIN>/api/auth/github/callback`, checked at startup |
| `OPERATOR_GITHUB_IDS` | yes | an operator input: comma separated numeric GitHub user ids, at least one; sets `is_operator` at sign-in |
| `OAUTH_STATE_KEY` | yes | 32 bytes, base64; seals `sa_oauth` |
| `UMAMI_URL`, `UMAMI_ADMIN_PASSWORD` | yes | existing keys (this stage) |
| `UMAMI_USER_PASSWORD_KEY` | yes | 32 bytes, base64; derives daily Umami passwords; shared with `jobs` |
| `TRACKER_RELEASE_DIR` | yes | existing key; holds `manifest.json` and `revoked.json` |
| `TRACKER_NPM_PACKAGE` | no | must stay unset until this stage defines the npm snippet (4.3.6); absent means `npm: null` |
| `NATS_URL`, `NATS_REPLICAS` | yes | existing keys |
| `NATS_USER`, `NATS_PASSWORD` | when the server requires auth | existing keys |
| database keys | yes | as `internal/db` `PoolConfig` for `cp_system`, `cp_tenant` and, from this stage, `cp_operator` |

In builds with tag `oauthfixture` only: `OAUTH_FIXTURE_URL` replaces the three
GitHub URLs. Production builds have no such key.

<!-- this stage appends "## 5. this stage: operator governance" below this line -->

## 5. this stage: operator governance

the design. Research and decisions: `docs/reference/the reference docs`.
New tables and columns (`cp.tenant_removals`, `cp.sites.status_reason`) come from
the this stage migrations listed in that file, section 6.

### 5.1 Operator identity

- `cp.users.is_operator` is set from `OPERATOR_GITHUB_IDS` at every sign-in
  (4.1.2, unchanged) and once at control plane start: one statement under
  `cp_system` sets it to true for listed `github_id` values and false for every
  other row. Each changed user gets one audit row `user.operator_sync` (actor
  `system:startup`, target `user:<id>`, detail `{"is_operator": {"old", "new"}}`).
- Every route under `/api/operator/` is **session** and **operator**: the session
  user must have `cp.users.is_operator = true` (read per request) and a
  `github_id` in the loaded `OPERATOR_GITHUB_IDS`. Otherwise **403
  `operator_required`**, for every method and for unknown ids alike. Removing an id
  takes effect at that person's next sign-in or the next control plane start,
  whichever comes first.
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
(site keys), `otlp_tokens` (this stage: revokes every token row and flips every
OTLP key to `revoked`, 6.4), `umami` (both
websites of every site with their data, the team, then Umami users whose console
user has no membership in any non-removed tenant), `web_ingest`
(`cp.web_ingest_hourly` rows; skipped when `keep_aggregates`), `api` (this stage:
`api.purge_tenant`; minute rows and writer state always, hourly, daily and
warning rows unless `keep_aggregates`),
`agg` (this stage; skipped when `keep_aggregates`). Each finished store writes
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
the operator check (cp_tenant has no right on `status` or `enrollment_answers`, on
purpose), with `where status = 'rejected'` in the update. Sets
`enrollment_answers`, the optional fields, `status = 'pending'`,
`status_reason = null`, `status_changed_at = now()`. Audit `tenant.resubmit`
(detail: changed fields old and new). Terms rule of 4.2.6 applies. **200**
Tenant. The tenant appears in the review queue (5.3 `review=true`).

### 5.8 Site domain verification

Proves control of `cp.sites.domain`. Beyond-RFP; never gates ingestion.

`GET /api/sites/{site_id}/verification`: session, member of the site's tenant.

```json
{
  "site_id": "uuid",
  "domain": "example.org",
  "verify_token": "43 characters base64url",
  "dns": {
    "name": "_stellar-analytics.example.org",
    "type": "TXT",
    "value": "stellar-analytics-verify=<verify_token>"
  },
  "file": {
    "url": "https://example.org/.well-known/stellar-analytics.txt",
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
  (the reference docs section 3), status 200, body at most 1,024 bytes, equal to the
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

### 5.9 `GET /api/operator/audit`

Operator. List envelope of AuditEntry, ordered by `(at desc, id desc)`, under
`cp_system`.

```json
{ "id": 1234, "at": "...", "actor": "user:uuid", "actor_login": "octocat",
  "action": "tenant.suspend", "target": "tenant:uuid",
  "detail": { "tenant_id": "uuid", "status": { "old": "active", "new": "suspended" }, "reason": "..." } }
```

- `id` is the `cp.audit_log` identity (a number). `actor_login` is the
  `github_login` for `user:` actors, else `null`.
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
`stellar_toml_checked_at`. Beyond-RFP evidence for operators; it decides
nothing.

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
  `api.metrics_daily` and `api.ingest_warnings` rows and, from this stage, `agg`
  rows of the removed tenant. Umami websites, their raw data and `api` minute rows are always
  deleted. The open SCF question behind the default is recorded in
  `docs/runbooks/tenant-removal.md`.

### 5.14 Configuration keys (this stage)

One new required key: `PLATFORM_DB_CP_OPERATOR_PASSWORD`, because operator reads run
as `cp_operator` (5.1). `OPERATOR_GITHUB_IDS` (4.5) now also drives the startup
sync of 5.1. The `jobs` binary gains the task `verify-domains` with no new key;
the request-forgery guard has no configuration key that could allow private
addresses (tests use a `_test.go` hook only).

---

## 6. this stage: OTLP tokens

Tenants create bearer tokens for the OTLP collector (`otlp.<BASE_DOMAIN>`). The
collector reads `registry_otlp` and accepts a token only while its
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
  the metrics writer (this stage).

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

## 7. this stage: API metrics

A tenant's own API traffic, read from the this stage tables `api.metrics_minute`,
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
`hour` (`api.metrics_hourly`) or `day` (`api.metrics_daily`). Only the table of
that resolution is read; resolutions are never mixed in one answer.

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

This is a documented rule, not a fallback: the answer always says which table it
read. Hourly and daily rows are recomputed by the `api-rollup` job every 5
minutes, so the newest few minutes of an `hour` or `day` answer can be missing
until the next rollup; the console says "updated every few minutes".

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
inside the range uses metric rows for the whole range; its span rows are left
out and `span_data_ignored` says so. The selection is
`internal/controlplane/apimetrics/source.go`, shared with phases 11 and 12.

**Source object**, one per service with any row in the range, ordered by
`service`, in every answer except warnings:

```json
{ "service": "payments-api", "source": "span", "span_data_ignored": false }
```

- `source`: `metric` or `span`. The console shows a "from traces" badge for
  `span` with one line: "Derived from sampled traces; counts are lower than the
  real request count when traces are sampled."
- `span_data_ignored`: true when the service also has span rows in the range
  that were not used because metric rows exist. The console notes "this service
  switched to metrics during the range; earlier trace-derived data is not shown".

The selection and the aggregate are two statements in one read committed
transaction; a row committed between them can be counted, but a service's
source cannot change between them in a way that mixes sources, because the
aggregate is restricted to the selected source of each service.

### 7.4 Numbers

- `requests`: `sum(count)` of the selected rows.
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
  non-canonical histogram layout (this stage), so percentiles are approximate. The
  console shows "approximate" next to such percentiles.

**Summary object:**

```json
{
  "requests": 1200, "errors_4xx": 30, "errors_5xx": 2,
  "rate_4xx": 0.025, "rate_5xx": 0.0016666666666666668,
  "p50": 0.042, "p95": 0.31, "p99": ">10 s", "lossy": false
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
  "previous": { "range": { ... }, "sources": [ ... ], "summary": { ... } }
}
```

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
      "rate_4xx": 0.019230769230769232, "rate_5xx": 0, "p50": 0.031, "p95": 0.2, "p99": 0.48, "lossy": false }
  ]
}
```

Each point's percentiles come from the bucket arrays of that time bucket only.

### 7.7 `GET .../api-metrics/routes`

Parameters: range, filters, `sort`, `order`, `limit`, `cursor`. One item per
service, method and route with at least one selected row.

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
      "p50": 0.04, "p95": 0.3, "p99": 0.9, "lossy": false }
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
- `codes`: every status code with requests, ascending.

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
      "requests": 1200, "lossy": false }
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
    { "kind": "secret_in_path", "count": 1, "last_day": "2026-11-11" }
  ],
  "days": [
    { "day": "2026-11-11", "kind": "secret_in_path", "count": 1 }
  ]
}
```

- `totals`: always these four kinds, in this order. `count` is the sum over the
  days, `last_day` the latest day with a count.
- `days`: every non-zero day and kind, ordered by day, then kind.
- Kinds (this stage writer): `span_derived` (points derived from spans were
  stored), `lossy_histogram` (a histogram was re-bucketed inexactly),
  `cardinality_overflow` (routes or services above the daily cap were folded into
  `{other}`), `secret_in_path` (a Stellar secret seed appeared in a request path
  and was redacted). The console shows a banner per kind with a non-zero count.

### 7.11 Configuration keys (this stage)

Two keys become required for the control plane, with the same values as for
`jobs`, because the retention step of 7.2 must match what the retention job
keeps: `API_MINUTE_RETENTION_DAYS` (at least 2) and
`API_HOURLY_RETENTION_MONTHS` (at least 24), loaded by
`config.LoadAPIRetention`.

---

## 8. this stage: global web and API views

the design. Operators compare every enrolled project on aggregates only.
Every route here is **session** and **operator** (5.1), runs as `cp_operator`
in one read-only, repeatable-read transaction, and reads only `agg.web_daily`,
`agg.web_daily_dim`, `api.metrics_daily`, `cp.web_ingest_hourly`, `cp.tenants`
and `cp.operator_sites`. `cp_operator` cannot read `api.metrics_minute`, any
`cp` table outside 4.7, or connect to the `umami` database, and an operator
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
  `enrolled_from`, or `q` out of range.

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
  `ingest_rejected`, `rejected_share`.

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
  "services": 3, "routes": 41, "span_services": 1, "source": "mixed", "lossy": false
}
```

- `requests` is the sum of `count`; `requests_4xx` and `requests_5xx` count
  status codes 400 to 499 and 500 to 599 (rows without a status code count
  only in `requests`). Rates are `null` without requests.
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

The purge step `agg` (after `api`, the design runner) deletes the tenant's
`agg.web_daily` and `agg.web_daily_dim` rows through the security definer
function `agg.purge_tenant`, the only right `cp_system` has in `agg`. With
`keep_aggregates` the step records `{"skipped": "keep_aggregates"}` and every
row stays (5.13). The function takes the tenant's advisory lock exclusively;
`aggregate-web` holds it shared for each write and skips removed tenants, so
rows of a purged tenant are never written again.

### 8.8 Configuration (this stage)

No new key. `jobs` gains the task `aggregate-web` (hourly: today and the 3
days before) and `jobs run aggregate-web --from <yyyy-mm-dd> --to <yyyy-mm-dd>`
for a backfill; both read Umami with `UMAMI_URL` and `UMAMI_ADMIN_PASSWORD`,
which `jobs` already requires.

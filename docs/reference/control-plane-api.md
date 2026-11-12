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
| 404 | `not_found` | unknown id, malformed id, or not a member |
| 405 | `method_not_allowed` | method not defined for the path |
| 409 | `slug_taken` | `cp.tenants.slug` already exists |
| 409 | `already_member` | invite or add for a user who is a member |
| 409 | `invite_exists` | an open invite for that login exists (`invites_tenant_login_idx`) |
| 409 | `last_owner` | removing or demoting the last owner |
| 409 | `site_removed` | change to a site with `status = 'removed'` |
| 409 | `terms_version_mismatch` | accepted version is not the current one |
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
| database keys | yes | as `internal/db` `PoolConfig` for `cp_system` and `cp_tenant` |

In builds with tag `oauthfixture` only: `OAUTH_FIXTURE_URL` replaces the three
GitHub URLs. Production builds have no such key.

<!-- this stage appends "## 5. this stage: operator governance" below this line -->

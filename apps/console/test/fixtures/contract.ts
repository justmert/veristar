// response bodies recorded from docs/reference/control-plane-api.md, with the
// placeholders of the contract ("uuid", "...") filled with values of the right
// shape. test fixtures only; product code never imports this file.

export const USER_ID = "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10";
export const OTHER_USER_ID = "0193a6f2-8a00-7c11-8d3e-1f0a2b3c4d5e";
export const TENANT_ID = "0193a6f3-0101-7a22-b3c4-d5e6f7a8b9c0";
export const SITE_ID = "0193a6f4-1111-7b33-8c44-d55e66f77a88";
export const QUARANTINE_ID = "0193a6f4-2222-7c44-9d55-e66f77a88b99";
export const INVITE_ID = "0193a6f5-3333-7d55-ae66-f77a88b99caa";

// valid strkeys produced with github.com/stellar/go-stellar-sdk/strkey v0.8.0
export const G_KEY = "GCW5LRWLKXKPMSXA7HXXT65FRLFXXANJJJFAZGVUER7MXP7BUWWKNRDL";
export const C_KEY = "CCW5LRWLKXKPMSXA7HXXT65FRLFXXANJJJFAZGVUER7MXP7BUWWKMVGS";
export const M_KEY = "MCW5LRWLKXKPMSXA7HXXT65FRLFXXANJJJFAZGVUER7MXP7BUWWKMAAAAAAAAAAAFIV2W";
// the same keys with the last character changed, rejected by that library
export const G_BAD_CHECKSUM = "GCW5LRWLKXKPMSXA7HXXT65FRLFXXANJJJFAZGVUER7MXP7BUWWKNRDA";

export const TERMS_VERSION = "2026-11-12-draft.1";

// 4.1.4
export const meBody = {
  user: {
    id: USER_ID,
    github_id: 1234567,
    github_login: "octocat",
    stellar_address: null,
    passkey_address: null,
    display_name: "octocat",
    is_operator: false,
    operator_role: null,
  },
  tenants: [
    {
      id: TENANT_ID,
      slug: "stellar-dex",
      name: "Stellar DEX",
      status: "active",
      role: "owner",
      accepted_terms_version: TERMS_VERSION,
      terms_current: true,
    },
  ],
  terms: { current_version: TERMS_VERSION, status: "draft", url: "/terms" },
  removed_tenants: [],
};

// 4.1.5
export const termsBody = {
  version: TERMS_VERSION,
  status: "draft",
  markdown: `---\nversion: ${TERMS_VERSION}\nstatus: draft\n---\n# Terms\n\n> Draft, not legally reviewed.\n\nNo fees to **anyone**.\n`,
};

// 3.1 tenant
export const tenantBody = {
  id: TENANT_ID,
  slug: "stellar-dex",
  name: "Stellar DEX",
  description: "",
  status: "active",
  status_reason: null,
  enrollment_answers: {
    website: "https://example.org",
    stellar_usage: "Soroban AMM on mainnet",
    stellar_ids: [G_KEY, C_KEY],
    scf_round_url: null,
  },
  accepted_terms_version: TERMS_VERSION,
  provisioning: "ready",
  role: "owner",
  created_at: "2026-11-12T09:00:00Z",
  updated_at: "2026-11-12T09:00:00Z",
  status_changed_at: "2026-11-12T09:00:00Z",
};

export const memberBody = {
  user_id: USER_ID,
  github_id: 1234567,
  github_login: "octocat",
  stellar_address: null,
  passkey_address: null,
  display_name: "octocat",
  role: "owner",
  created_at: "2026-11-12T09:00:00Z",
};

export const inviteBody = {
  id: INVITE_ID,
  github_id: 7654321,
  github_login: "someone",
  stellar_address: null,
  display_name: "someone",
  role: "member",
  created_by: USER_ID,
  created_at: "2026-11-12T09:00:00Z",
  expires_at: "2026-11-26T09:00:00Z",
};

// 4.2.5
export const membersBody = { members: [memberBody], invites: [inviteBody] };
export const addMemberAsMemberBody = {
  kind: "member",
  member: {
    ...memberBody,
    user_id: OTHER_USER_ID,
    github_login: "hubot",
    display_name: "hubot",
    role: "member",
  },
};
export const addMemberAsInviteBody = { kind: "invite", invite: inviteBody };

// 3.1 site
export const siteBody = {
  id: SITE_ID,
  tenant_id: TENANT_ID,
  name: "Marketing site",
  domain: "example.org",
  origins: ["https://example.org", "https://www.example.org"],
  quarantine_website_id: QUARANTINE_ID,
  status: "active",
  status_reason: null,
  in_review: false,
  verification: "unchecked",
  verify_method: null,
  verified_at: null,
  last_verify_at: null,
  last_verify_error: null,
  stellar_toml: null,
  stellar_toml_checked_at: null,
  collect_utm: false,
  created_at: "2026-11-12T09:00:00Z",
  updated_at: "2026-11-12T09:00:00Z",
};

// 2.3 list envelope
export const sitePageBody = { items: [siteBody], next_cursor: null };

// 4.3.6
export const snippetBody = {
  site_id: SITE_ID,
  version: "1.0.0",
  src: "https://in.example.net/v/1.0.0/sdk.js",
  integrity: "sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j",
  tag: `<script defer src="https://in.example.net/v/1.0.0/sdk.js" integrity="sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j" crossorigin="anonymous" data-website-id="${SITE_ID}"></script>`,
  npm: null,
  versions: [{ version: "1.0.0", released: "2026-11-12", bytes: 6734, gzip_bytes: 3273 }],
};

// 4.3.7
export const ingestBody = {
  site_id: SITE_ID,
  from: "2026-11-11T09:00:00Z",
  to: "2026-11-12T09:00:00Z",
  bucket: "hour",
  series: [
    {
      at: "2026-11-11T09:00:00Z",
      counts: { accepted: 120, quarantined: 3, rejected_origin: 2 },
    },
  ],
  totals: { accepted: 2810, quarantined: 41, bot: 9, dlq: 0, rejected_origin: 17 },
};

// 2.2
export const validationErrorBody = {
  error: {
    code: "validation_failed",
    message: "origins[1] must be a bare https origin",
    fields: { "origins[1]": "not_bare_origin" },
    request_id: "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10",
  },
};

export function errorBody(code: string, fields?: Record<string, string>) {
  return {
    error: {
      code,
      message: code.replaceAll("_", " "),
      ...(fields ? { fields } : {}),
      request_id: "0193a6f9-0000-7000-8000-000000000001",
    },
  };
}

// ---- contract section 5 ----

export const OPERATOR_ID = "0193a6f6-4444-7e66-bf77-a88b99caadbb";
export const REMOVED_TENANT_ID = "0193a6f7-5555-7f77-8a88-b99caadbbecc";

// 5.10 /api/me of a member of a removed project
export const meWithRemovedBody = {
  ...meBody,
  removed_tenants: [
    {
      id: REMOVED_TENANT_ID,
      name: "Old Project",
      status_reason: "Not a Stellar project",
      status_changed_at: "2026-11-12T10:00:00Z",
    },
  ],
};

export const operatorMeBody = {
  ...meBody,
  user: {
    id: OPERATOR_ID,
    github_id: 1,
    github_login: "sa-operator",
    stellar_address: null,
    passkey_address: null,
    display_name: "sa-operator",
    is_operator: true,
    operator_role: "operator_admin",
  },
  tenants: [],
};

// 5.12
export const stellarTomlFound = {
  status: "found",
  error: null,
  http_status: 200,
  bytes: 2048,
  version: "2.7.0",
  network_passphrase: "Public Global Stellar Network ; September 2015",
  network: "public",
  accounts_count: 3,
  accounts_valid: 3,
  signing_key_present: true,
  signing_key_valid: true,
  org_url: "https://example.org",
  org_url_same_domain: true,
  currencies: [
    { code: "USDX", issuer: G_KEY, contract: null, valid: true },
    { code: "TKN", issuer: null, contract: C_KEY, valid: true },
  ],
  currencies_total: 2,
};

export const stellarTomlNotFound = {
  status: "not_found",
  error: null,
  http_status: 404,
  bytes: null,
  version: null,
  network_passphrase: null,
  network: null,
  accounts_count: 0,
  accounts_valid: 0,
  signing_key_present: false,
  signing_key_valid: false,
  org_url: null,
  org_url_same_domain: false,
  currencies: [],
  currencies_total: 0,
};

// 5.10 site with the verification, review and stellar.toml fields filled
export const siteVerifiedBody = {
  ...siteBody,
  verification: "verified",
  verify_method: "dns",
  verified_at: "2026-11-12T09:30:00Z",
  last_verify_at: "2026-11-12T09:30:00Z",
  stellar_toml: stellarTomlFound,
  stellar_toml_checked_at: "2026-11-12T09:05:00Z",
};

// 5.2
export const operatorSummaryBody = {
  id: TENANT_ID,
  slug: "stellar-dex",
  name: "Stellar DEX",
  status: "pending",
  status_reason: null,
  website: "https://example.org",
  created_at: "2026-11-12T09:00:00Z",
  status_changed_at: "2026-11-12T09:00:00Z",
  site_count: 2,
  sites_in_review: 0,
  sites_verified: 1,
  sites_with_stellar_toml: 1,
};

export const operatorSitePageBody = { items: [operatorSummaryBody], next_cursor: "Y3Vyc29y" };

export const operatorSiteBody = {
  id: SITE_ID,
  name: "Marketing site",
  domain: "example.org",
  origins: ["https://example.org"],
  status: "active",
  status_reason: null,
  in_review: false,
  domain_previously_removed: false,
  verification: "verified",
  verify_method: "dns",
  verified_at: "2026-11-12T09:30:00Z",
  last_verify_at: "2026-11-12T09:30:00Z",
  last_verify_error: null,
  stellar_toml: stellarTomlFound,
  stellar_toml_checked_at: "2026-11-12T09:05:00Z",
  created_at: "2026-11-12T09:00:00Z",
  updated_at: "2026-11-12T09:00:00Z",
};

// 5.9
export const auditEntryBody = {
  id: 1234,
  at: "2026-11-12T10:00:00Z",
  actor: `user:${OPERATOR_ID}`,
  actor_login: "sa-operator",
  actor_display_name: "sa-operator",
  action: "tenant.suspend",
  target: `tenant:${TENANT_ID}`,
  target_display_name: "Stellar DEX",
  detail: {
    tenant_id: TENANT_ID,
    status: { old: "active", new: "suspended" },
    reason: "Spam traffic",
  },
};

export const auditPageBody = {
  items: [
    auditEntryBody,
    {
      id: 1200,
      at: "2026-11-12T09:00:00Z",
      actor: "system:umami-rotate",
      actor_login: null,
      actor_display_name: null,
      action: "umami.password_rotate",
      target: "",
      target_display_name: null,
      detail: { day: "2026-11-12", users: 4 },
    },
  ],
  next_cursor: null,
};

// 5.4
export const operatorDetailBody = {
  tenant: {
    id: TENANT_ID,
    slug: "stellar-dex",
    name: "Stellar DEX",
    description: "An AMM",
    status: "pending",
    status_reason: null,
    enrollment_answers: {
      website: "https://example.org",
      stellar_usage: "Soroban AMM on mainnet",
      stellar_ids: [G_KEY, G_BAD_CHECKSUM],
      scf_round_url: null,
    },
    accepted_terms_version: TERMS_VERSION,
    provisioning: "pending",
    created_by: {
      id: USER_ID,
      github_id: 1234567,
      github_login: "octocat",
      stellar_address: null,
      passkey_address: null,
      display_name: "octocat",
    },
    approved_by: null,
    created_at: "2026-11-12T09:00:00Z",
    updated_at: "2026-11-12T09:00:00Z",
    status_changed_at: "2026-11-12T09:00:00Z",
  },
  stellar_ids: [
    { id: G_KEY, type: "account", valid: true },
    { id: G_BAD_CHECKSUM, type: null, valid: false },
  ],
  members: [
    {
      github_id: 1234567,
      github_login: "octocat",
      stellar_address: null,
      passkey_address: null,
      display_name: "octocat",
      role: "owner",
      created_at: "2026-11-12T09:00:00Z",
    },
  ],
  sites: [operatorSiteBody],
  ingest: {
    last_24h: { accepted: 2810, quarantined: 41, rejected_origin: 17 },
    last_30d: { accepted: 90210 },
  },
  removal: null,
  recent_audit: [auditEntryBody],
  allowed_actions: ["approve", "reject", "remove"],
};

export const removedDetailBody = {
  ...operatorDetailBody,
  tenant: { ...operatorDetailBody.tenant, status: "removed", status_reason: "Fraud" },
  removal: {
    requested_at: "2026-11-12T11:00:00Z",
    requested_by: OPERATOR_ID,
    requested_by_login: "sa-operator",
    requested_by_display_name: "sa-operator",
    keep_aggregates: false,
    completed_at: null,
    steps: { registry: { done_at: "2026-11-12T11:00:05Z" } },
  },
  allowed_actions: [],
};

// 5.8
export const verificationBody = {
  site_id: SITE_ID,
  domain: "example.org",
  verify_token: "q8m3xK2vN0pZr7Tg5LhW1yBc4FdJ6sEu9AoRiVkXnMQ",
  dns: {
    name: "_veristar.example.org",
    type: "TXT",
    value: "veristar-verify=q8m3xK2vN0pZr7Tg5LhW1yBc4FdJ6sEu9AoRiVkXnMQ",
  },
  file: {
    url: "https://example.org/.well-known/veristar.txt",
    body: "q8m3xK2vN0pZr7Tg5LhW1yBc4FdJ6sEu9AoRiVkXnMQ",
  },
  verification: "unverified",
  verify_method: null,
  verified_at: null,
  last_verify_at: "2026-11-12T09:10:00Z",
  last_verify_error: "dns_no_record",
};

// 6.1 and 6.2
export const TOKEN_ID = "0193a6fa-4444-7e66-bf77-a88b99caadbb";
export const OTLP_PLAIN = "sa_otlp_Ab3x9QnRZtK2mW8vLp0sYcD4hJ7uE1fGiO5kNqBa";
export const otlpTokenBody = {
  id: TOKEN_ID,
  tenant_id: TENANT_ID,
  name: "Production API",
  environment: "production",
  prefix: "sa_otlp_Ab3x",
  status: "active",
  created_by: USER_ID,
  created_at: "2026-11-12T09:00:00Z",
  revoked_at: null,
  last_seen_at: null,
};
export const otlpTokenCreatedBody = { ...otlpTokenBody, token: OTLP_PLAIN };

// section 7: api metrics. the range, source, summary and item objects are the
// examples of 7.2 to 7.10; lists are filled out to the shapes the text gives.
export const apiRangeBody = {
  preset: "24h",
  from: "2026-11-11T10:00:00Z",
  to: "2026-11-12T10:00:00Z",
  resolution: "hour",
  bucket_seconds: 3600,
  resolution_note: null,
};
export const apiPreviousRangeBody = {
  preset: "previous",
  from: "2026-11-10T10:00:00Z",
  to: "2026-11-11T10:00:00Z",
  resolution: "hour",
  bucket_seconds: 3600,
  resolution_note: null,
};
export const apiSourceMetric = {
  service: "payments-api",
  source: "metric",
  span_data_ignored: false,
};
export const apiSourceSpan = { service: "orders-worker", source: "span", span_data_ignored: false };
export const apiSummaryBody = {
  requests: 1200,
  errors_4xx: 30,
  errors_5xx: 2,
  rate_4xx: 0.025,
  rate_5xx: 0.0016666666666666668,
  p50: 0.042,
  p95: 0.31,
  p99: ">10 s",
  lossy: false,
  infrastructure_requests: 8640,
};
export const apiPreviousSummaryBody = {
  requests: 1000,
  errors_4xx: 20,
  errors_5xx: 5,
  rate_4xx: 0.02,
  rate_5xx: 0.005,
  p50: 0.05,
  p95: 0.25,
  p99: 0.9,
  lossy: false,
  infrastructure_requests: 0,
};
export const apiOverviewBody = {
  current: { range: apiRangeBody, sources: [apiSourceMetric], summary: apiSummaryBody },
  previous: {
    range: apiPreviousRangeBody,
    sources: [apiSourceMetric],
    summary: apiPreviousSummaryBody,
  },
  traffic_classes: { rule_set_version: 1, applied_from: "2026-11-16T09:00:00Z" },
};
export const apiEmptySummaryBody = {
  requests: 0,
  errors_4xx: 0,
  errors_5xx: 0,
  rate_4xx: null,
  rate_5xx: null,
  p50: null,
  p95: null,
  p99: null,
  lossy: false,
  infrastructure_requests: 0,
};

// 7.6: one point per bucket of the 24 hour range, empty buckets included
export const apiTimeseriesBody = {
  range: apiRangeBody,
  sources: [apiSourceMetric],
  points: Array.from({ length: 24 }, (_, i) => {
    const at = new Date(Date.parse("2026-11-11T10:00:00Z") + i * 3600_000)
      .toISOString()
      .replace(".000Z", "Z");
    if (i === 23)
      return {
        at,
        requests: 52,
        errors_4xx: 1,
        errors_5xx: 0,
        rate_4xx: 0.019230769230769232,
        rate_5xx: 0,
        p50: 0.031,
        p95: 0.2,
        p99: 0.48,
        lossy: false,
        infrastructure_requests: 0,
      };
    if (i === 22)
      return {
        at,
        requests: 3,
        errors_4xx: 0,
        errors_5xx: 1,
        rate_4xx: 0,
        rate_5xx: 0.3333333333333333,
        p50: 2,
        p95: ">10 s",
        p99: ">10 s",
        lossy: true,
        infrastructure_requests: 0,
      };
    return { at, ...apiEmptySummaryBody };
  }),
};

export const apiRouteItemBody = {
  service: "payments-api",
  method: "GET",
  route: "/v1/accounts/{id}",
  source: "metric",
  requests: 930,
  errors_4xx: 12,
  errors_5xx: 0,
  rate_4xx: 0.012903225806451613,
  rate_5xx: 0,
  p50: 0.04,
  p95: 0.3,
  p99: 0.9,
  lossy: false,
  infrastructure_requests: 0,
};
export const apiSpanRouteItemBody = {
  service: "orders-worker",
  method: "POST",
  route: "/jobs/{id}",
  source: "span",
  requests: 40,
  errors_4xx: 0,
  errors_5xx: 1,
  rate_4xx: 0,
  rate_5xx: 0.025,
  p50: 0.12,
  p95: 0.8,
  p99: ">10 s",
  lossy: false,
  infrastructure_requests: 0,
};
export const ROUTES_CURSOR = "eyJyIjoicm91dGVzIiwibyI6NTAsImgiOiJhYmMifQ";
export const apiRoutesBody = {
  range: apiRangeBody,
  sources: [apiSourceSpan, apiSourceMetric],
  total: 134,
  items: [apiRouteItemBody, apiSpanRouteItemBody],
  next_cursor: ROUTES_CURSOR,
};

export const apiStatusBody = {
  range: apiRangeBody,
  sources: [apiSourceMetric],
  requests: 1200,
  infrastructure_requests: 8640,
  classes: [
    { class: "1xx", requests: 0 },
    { class: "2xx", requests: 1150 },
    { class: "3xx", requests: 18 },
    { class: "4xx", requests: 30 },
    { class: "5xx", requests: 2 },
    { class: "unknown", requests: 0 },
  ],
  codes: [
    { status_code: 200, class: "2xx", requests: 1100 },
    { status_code: 201, class: "2xx", requests: 50 },
    { status_code: 304, class: "3xx", requests: 18 },
    { status_code: 404, class: "4xx", requests: 30 },
    { status_code: 503, class: "5xx", requests: 2 },
  ],
};

export const apiServicesBody = {
  range: apiRangeBody,
  items: [
    {
      service: "orders-worker",
      source: "span",
      span_data_ignored: false,
      requests: 40,
      lossy: false,
      infrastructure_requests: 0,
    },
    {
      service: "payments-api",
      source: "metric",
      span_data_ignored: false,
      requests: 1200,
      lossy: false,
      infrastructure_requests: 0,
    },
  ],
};
export const apiServicesEmptyBody = { range: apiRangeBody, items: [] };

export const apiWarningsBody = {
  range: apiRangeBody,
  totals: [
    { kind: "span_derived", count: 0, last_day: null },
    { kind: "lossy_histogram", count: 4, last_day: "2026-11-12" },
    { kind: "cardinality_overflow", count: 0, last_day: null },
    { kind: "secret_in_path", count: 1, last_day: "2026-11-11" },
    { kind: "coarse_buckets", count: 4, last_day: "2026-11-12" },
  ],
  days: [
    { day: "2026-11-11", kind: "secret_in_path", count: 1 },
    { day: "2026-11-12", kind: "coarse_buckets", count: 4 },
    { day: "2026-11-12", kind: "lossy_histogram", count: 4 },
  ],
};
export const apiWarningsNoneBody = {
  range: apiRangeBody,
  totals: [
    { kind: "span_derived", count: 0, last_day: null },
    { kind: "lossy_histogram", count: 0, last_day: null },
    { kind: "cardinality_overflow", count: 0, last_day: null },
    { kind: "secret_in_path", count: 0, last_day: null },
    { kind: "coarse_buckets", count: 0, last_day: null },
  ],
  days: [],
};

// ---- contract section 10 ----
export const ERASURE_ID = "0193a6fa-6666-7a88-9b99-caadbbeccfdd";
export const SESSION_ID = "2f1c7a9e-5b3d-4c8e-9a1f-6d0b2e4c8a7f";

// 10.1 as the contract's example
export const erasureDoneBody = {
  id: ERASURE_ID,
  tenant_id: TENANT_ID,
  site_id: SITE_ID,
  kind: "visitor_today",
  status: "done",
  outcome: "erased",
  message:
    "Deleted 2 session(s) with 4 event(s) of this visitor on 2026-11-12 (UTC) and 0 undeliverable queued message(s).",
  day: "2026-11-12",
  session_id: null,
  requested_by: USER_ID,
  requested_at: "2026-11-12T10:00:00Z",
  completed_at: "2026-11-12T10:00:03Z",
  sessions_deleted: 2,
  events_deleted: 4,
  dlq_messages_deleted: 0,
  affected_from: "2026-11-12",
  affected_to: "2026-11-12",
  reaggregated_at: null,
  error: null,
};

export const erasureRunningBody = {
  ...erasureDoneBody,
  status: "running",
  outcome: null,
  message: null,
  completed_at: null,
  sessions_deleted: 0,
  events_deleted: 0,
  affected_from: null,
  affected_to: null,
};

// 5.11 the step-up status of a session that holds a fresh step-up
export const steppedUpBody = {
  stepped_up: true,
  stepped_up_at: "2026-11-16T09:00:00Z",
  expires_at: "2026-11-16T21:00:00Z",
  max_age_seconds: 43200,
  passkeys: 1,
  sign_in_passkey: false,
  enrollment: "closed",
  enrollment_until: null,
};

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
  user: { id: USER_ID, github_id: 1234567, github_login: "octocat", is_operator: false },
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
  role: "owner",
  created_at: "2026-11-12T09:00:00Z",
};

export const inviteBody = {
  id: INVITE_ID,
  github_login: "someone",
  role: "member",
  created_by: USER_ID,
  created_at: "2026-11-12T09:00:00Z",
  expires_at: "2026-11-26T09:00:00Z",
};

// 4.2.5
export const membersBody = { members: [memberBody], invites: [inviteBody] };
export const addMemberAsMemberBody = {
  kind: "member",
  member: { ...memberBody, user_id: OTHER_USER_ID, github_login: "hubot", role: "member" },
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

// ---- this stage, contract section 5 ----

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
  user: { id: OPERATOR_ID, github_id: 1, github_login: "sa-operator", is_operator: true },
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

// 5.10 site with the this stage fields filled
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
  action: "tenant.suspend",
  target: `tenant:${TENANT_ID}`,
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
      action: "umami.password_rotate",
      target: "",
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
    created_by: { id: USER_ID, github_id: 1234567, github_login: "octocat" },
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
    name: "_stellar-analytics.example.org",
    type: "TXT",
    value: "stellar-analytics-verify=q8m3xK2vN0pZr7Tg5LhW1yBc4FdJ6sEu9AoRiVkXnMQ",
  },
  file: {
    url: "https://example.org/.well-known/stellar-analytics.txt",
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

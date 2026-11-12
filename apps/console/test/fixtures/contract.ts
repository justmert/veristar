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
  verify_method: null,
  verified_at: null,
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

// zod schemas for every control plane response the console reads, mirroring
// docs/reference/control-plane-api.md. objects are not strict: later phases
// add fields to these objects (the contract allows it), and an added field
// must not break an older console. a missing or mistyped field does fail.

import { z } from "zod";

// zod compiles object parsers with new Function unless told not to. the csp
// of deploy/caddy/app.Caddyfile has no 'unsafe-eval', so the compile (and
// zod's probe for it, which a browser reports as a violation) must not run.
// this runs before any schema is used, because every schema lives here.
z.config({ jitless: true });

const id = z.uuid();
const time = z.iso.datetime();

export const tenantStatus = z.enum(["pending", "active", "rejected", "suspended", "removed"]);
export type TenantStatus = z.infer<typeof tenantStatus>;

export const role = z.enum(["owner", "member"]);
export type Role = z.infer<typeof role>;

export const siteStatus = z.enum(["pending", "active", "suspended", "removed"]);
export type SiteStatus = z.infer<typeof siteStatus>;

// 3.1 user
export const user = z.object({
  id,
  github_id: z.number().int().positive(),
  github_login: z.string().min(1),
  is_operator: z.boolean(),
});
export type User = z.infer<typeof user>;

// 4.1.4 the tenant summary inside /api/me
export const meTenant = z.object({
  id,
  slug: z.string(),
  name: z.string(),
  status: tenantStatus,
  role,
  accepted_terms_version: z.string().nullable(),
  terms_current: z.boolean(),
});
export type MeTenant = z.infer<typeof meTenant>;

export const termsRef = z.object({
  current_version: z.string().min(1),
  status: z.string().min(1),
  url: z.string().min(1),
});

export const me = z.object({
  user,
  tenants: z.array(meTenant),
  terms: termsRef,
});
export type Me = z.infer<typeof me>;

// 4.1.5
export const terms = z.object({
  version: z.string().min(1),
  status: z.string().min(1),
  markdown: z.string(),
});
export type Terms = z.infer<typeof terms>;

export const enrollmentAnswers = z.object({
  website: z.string(),
  stellar_usage: z.string(),
  stellar_ids: z.array(z.string()),
  scf_round_url: z.string().nullable(),
});

// 3.1 tenant
export const tenant = z.object({
  id,
  slug: z.string(),
  name: z.string(),
  description: z.string(),
  status: tenantStatus,
  status_reason: z.string().nullable(),
  enrollment_answers: enrollmentAnswers,
  accepted_terms_version: z.string().nullable(),
  provisioning: z.enum(["pending", "ready"]),
  role,
  created_at: time,
  updated_at: time,
  status_changed_at: time,
});
export type Tenant = z.infer<typeof tenant>;

// 2.3 list envelope
export function page<T extends z.ZodType>(item: T) {
  return z.object({ items: z.array(item), next_cursor: z.string().nullable() });
}

export const tenantPage = page(tenant);

// 3.1 member and invite
export const member = z.object({
  user_id: id,
  github_id: z.number().int().positive(),
  github_login: z.string().min(1),
  role,
  created_at: time,
});
export type Member = z.infer<typeof member>;

export const invite = z.object({
  id,
  github_login: z.string().min(1),
  role,
  created_by: id,
  created_at: time,
  expires_at: time,
});
export type Invite = z.infer<typeof invite>;

export const members = z.object({ members: z.array(member), invites: z.array(invite) });
export type Members = z.infer<typeof members>;

// 4.2.5 add answers either a member or an invite
export const addMemberResult = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("member"), member }),
  z.object({ kind: z.literal("invite"), invite }),
]);
export type AddMemberResult = z.infer<typeof addMemberResult>;

// 3.1 site
export const site = z.object({
  id,
  tenant_id: id,
  name: z.string(),
  domain: z.string(),
  origins: z.array(z.string()),
  quarantine_website_id: id.nullable(),
  status: siteStatus,
  verify_method: z.enum(["dns", "file"]).nullable(),
  verified_at: time.nullable(),
  created_at: time,
  updated_at: time,
});
export type Site = z.infer<typeof site>;

export const sitePage = page(site);

// 4.3.6 snippet
export const trackerVersion = z.object({
  version: z.string().min(1),
  released: z.iso.date(),
  bytes: z.number().int().positive(),
  gzip_bytes: z.number().int().positive(),
});
export type TrackerVersion = z.infer<typeof trackerVersion>;

export const npmPackage = z.object({
  package: z.string().min(1),
  version: z.string().min(1),
  install: z.string().min(1),
  import: z.string().min(1),
});

export const snippet = z.object({
  site_id: id,
  version: z.string().min(1),
  src: z.url(),
  integrity: z.string().regex(/^sha384-[A-Za-z0-9+/]{64}$/),
  tag: z.string().startsWith("<script "),
  npm: npmPackage.nullable(),
  versions: z.array(trackerVersion).min(1),
});
export type Snippet = z.infer<typeof snippet>;

// 4.3.7 ingest outcome counts
const counts = z.record(z.string(), z.number().int().nonnegative());

export const ingest = z.object({
  site_id: id,
  from: time,
  to: time,
  bucket: z.enum(["hour", "day"]),
  series: z.array(z.object({ at: time, counts })),
  totals: counts,
});
export type Ingest = z.infer<typeof ingest>;

// 2.2 error envelope
export const errorEnvelope = z.object({
  error: z.object({
    code: z.string().min(1),
    message: z.string(),
    fields: z.record(z.string(), z.string()).optional(),
    request_id: z.string(),
  }),
});

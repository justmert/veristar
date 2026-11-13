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

// 5.10 projects of the user that an operator removed; every tenant route
// answers 404 for them, so this is the only place their reason shows up
export const removedTenant = z.object({
  id,
  name: z.string(),
  status_reason: z.string().nullable(),
  status_changed_at: time,
});
export type RemovedTenant = z.infer<typeof removedTenant>;

export const me = z.object({
  user,
  tenants: z.array(meTenant),
  terms: termsRef,
  removed_tenants: z.array(removedTenant),
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
  github_id: z.number().int().positive(),
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

export const verifyMethod = z.enum(["dns", "file"]);
export type VerifyMethod = z.infer<typeof verifyMethod>;

// 5.8 verified, unverified or never checked
export const verification = z.enum(["verified", "unverified", "unchecked"]);
export type Verification = z.infer<typeof verification>;

// 5.12 the stellar.toml signal. outside status "found" the contract leaves
// the read fields null, 0, false or empty, so scalars are nullable here
const nint = z.number().int().nonnegative().nullable();
export const stellarTomlCurrency = z.object({
  code: z.string(),
  issuer: z.string().nullable(),
  contract: z.string().nullable(),
  valid: z.boolean(),
});
export const stellarToml = z.object({
  status: z.enum(["found", "not_found", "invalid", "too_large", "refused", "error"]),
  error: z.string().nullable(),
  http_status: z.number().int().nullable(),
  bytes: nint,
  version: z.string().nullable(),
  network_passphrase: z.string().nullable(),
  network: z.enum(["public", "testnet", "futurenet", "other"]).nullable(),
  accounts_count: nint,
  accounts_valid: nint,
  signing_key_present: z.boolean().nullable(),
  signing_key_valid: z.boolean().nullable(),
  org_url: z.string().nullable(),
  org_url_same_domain: z.boolean().nullable(),
  currencies: z.array(stellarTomlCurrency).nullable(),
  currencies_total: nint,
});
export type StellarToml = z.infer<typeof stellarToml>;

// 3.1 site, with the this stage fields of 5.10
export const site = z.object({
  id,
  tenant_id: id,
  name: z.string(),
  domain: z.string(),
  origins: z.array(z.string()),
  quarantine_website_id: id.nullable(),
  status: siteStatus,
  status_reason: z.string().nullable(),
  in_review: z.boolean(),
  verification,
  verify_method: verifyMethod.nullable(),
  verified_at: time.nullable(),
  last_verify_at: time.nullable(),
  last_verify_error: z.string().nullable(),
  stellar_toml: stellarToml.nullable(),
  stellar_toml_checked_at: time.nullable(),
  // 3.1: the gateway keeps utm_* and ref only when this is on
  collect_utm: z.boolean(),
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

// 5.8 the verification instructions of one site
export const siteVerification = z.object({
  site_id: id,
  domain: z.string(),
  verify_token: z.string().min(1),
  dns: z.object({ name: z.string().min(1), type: z.literal("TXT"), value: z.string().min(1) }),
  file: z.object({ url: z.url(), body: z.string().min(1) }),
  verification,
  verify_method: verifyMethod.nullable(),
  verified_at: time.nullable(),
  last_verify_at: time.nullable(),
  last_verify_error: z.string().nullable(),
});
export type SiteVerification = z.infer<typeof siteVerification>;

// 5.2 operator objects
export const operatorTenantSummary = z.object({
  id,
  slug: z.string(),
  name: z.string(),
  status: tenantStatus,
  status_reason: z.string().nullable(),
  website: z.string(),
  created_at: time,
  status_changed_at: time,
  site_count: z.number().int().nonnegative(),
  sites_in_review: z.number().int().nonnegative(),
  sites_verified: z.number().int().nonnegative(),
  sites_with_stellar_toml: z.number().int().nonnegative(),
});
export type OperatorTenantSummary = z.infer<typeof operatorTenantSummary>;
export const operatorTenantPage = page(operatorTenantSummary);

export const operatorSite = z.object({
  id,
  name: z.string(),
  domain: z.string(),
  origins: z.array(z.string()),
  status: siteStatus,
  status_reason: z.string().nullable(),
  in_review: z.boolean(),
  domain_previously_removed: z.boolean(),
  verification,
  verify_method: verifyMethod.nullable(),
  verified_at: time.nullable(),
  last_verify_at: time.nullable(),
  last_verify_error: z.string().nullable(),
  stellar_toml: stellarToml.nullable(),
  stellar_toml_checked_at: time.nullable(),
  created_at: time,
  updated_at: time,
});
export type OperatorSite = z.infer<typeof operatorSite>;

export const tenantAction = z.enum(["approve", "reject", "suspend", "resume", "remove"]);
export type TenantAction = z.infer<typeof tenantAction>;

const userRef = z.object({
  id,
  github_id: z.number().int().positive(),
  github_login: z.string().min(1),
});

// 5.9
export const auditEntry = z.object({
  id: z.number().int(),
  at: time,
  actor: z.string(),
  actor_login: z.string().nullable(),
  action: z.string().min(1),
  target: z.string(),
  detail: z.record(z.string(), z.unknown()).nullable(),
});
export type AuditEntry = z.infer<typeof auditEntry>;
export const auditPage = page(auditEntry);

// 5.4
export const operatorTenantDetail = z.object({
  tenant: z.object({
    id,
    slug: z.string(),
    name: z.string(),
    description: z.string(),
    status: tenantStatus,
    status_reason: z.string().nullable(),
    enrollment_answers: enrollmentAnswers,
    accepted_terms_version: z.string().nullable(),
    provisioning: z.enum(["pending", "ready"]),
    created_by: userRef.nullable(),
    approved_by: userRef.nullable(),
    created_at: time,
    updated_at: time,
    status_changed_at: time,
  }),
  stellar_ids: z.array(
    z.object({
      id: z.string(),
      // an id that fails the strkey check has no type
      type: z.enum(["account", "muxed_account", "contract"]).nullable(),
      valid: z.boolean(),
    }),
  ),
  members: z.array(
    z.object({
      github_id: z.number().int().positive(),
      github_login: z.string().min(1),
      role,
      created_at: time,
    }),
  ),
  sites: z.array(operatorSite),
  ingest: z.object({ last_24h: counts, last_30d: counts }),
  removal: z
    .object({
      requested_at: time,
      requested_by: z.union([id, userRef]).nullable(),
      keep_aggregates: z.boolean(),
      completed_at: time.nullable(),
      steps: z.record(z.string(), z.object({ done_at: time })),
    })
    .nullable(),
  recent_audit: z.array(auditEntry),
  allowed_actions: z.array(tenantAction),
});
export type OperatorTenantDetail = z.infer<typeof operatorTenantDetail>;

// 5.7 and 5.13
export const enrollmentMode = z.object({ mode: z.enum(["open", "approval"]) });
export type EnrollmentMode = z.infer<typeof enrollmentMode>["mode"];
export const keepAggregates = z.object({ keep: z.boolean() });

// 6.1 otlp token; the plaintext is only in the create answer (6.2)
export const otlpTokenStatus = z.enum(["active", "revoked"]);
export const otlpToken = z.object({
  id,
  tenant_id: id,
  name: z.string(),
  environment: z.string(),
  prefix: z.string().startsWith("sa_otlp_"),
  status: otlpTokenStatus,
  created_by: id,
  created_at: time,
  revoked_at: time.nullable(),
  last_seen_at: time.nullable(),
});
export type OtlpToken = z.infer<typeof otlpToken>;
export const otlpTokenPage = page(otlpToken);

// the design: sa_otlp_ and 40 base62 characters
export const otlpTokenCreated = otlpToken.extend({
  token: z.string().regex(/^sa_otlp_[0-9A-Za-z]{40}$/),
});
export type OtlpTokenCreated = z.infer<typeof otlpTokenCreated>;

// 11.1 the live alerts of the alertmanager cluster (operators only)
export const alertSource = z.object({
  address: z.string().min(1),
  reachable: z.boolean(),
  watchdog_firing: z.boolean(),
  error: z.string().nullable(),
});
export type AlertSource = z.infer<typeof alertSource>;

export const alertState = z.enum(["active", "suppressed", "unprocessed"]);

export const firingAlert = z.object({
  fingerprint: z.string().min(1),
  name: z.string().min(1),
  severity: z.string().nullable(),
  summary: z.string().nullable(),
  description: z.string().nullable(),
  node: z.string().nullable(),
  instance: z.string().nullable(),
  service: z.string().nullable(),
  starts_at: time,
  state: alertState,
  inhibited_by: z.array(z.string()),
  runbook: z.string().nullable(),
  labels: z.record(z.string(), z.string()),
});
export type FiringAlert = z.infer<typeof firingAlert>;

export const operatorAlerts = z.object({
  checked_at: time,
  watchdog_firing: z.boolean(),
  sources: z.array(alertSource).min(1),
  alerts: z.array(firingAlert),
});
export type OperatorAlerts = z.infer<typeof operatorAlerts>;

// 2.2 error envelope
export const errorEnvelope = z.object({
  error: z.object({
    code: z.string().min(1),
    message: z.string(),
    fields: z.record(z.string(), z.string()).optional(),
    request_id: z.string(),
  }),
});

// 7.2 the range an api metrics answer was computed for
export const apiRangePreset = z.enum(["1h", "24h", "7d", "30d", "90d", "12mo", "24mo"]);
export type ApiRangePreset = z.infer<typeof apiRangePreset>;

export const apiRange = z.object({
  preset: z.union([apiRangePreset, z.literal("custom"), z.literal("previous")]),
  from: time,
  to: time,
  resolution: z.enum(["minute", "hour", "day"]),
  bucket_seconds: z.union([z.literal(60), z.literal(3600), z.literal(86400)]),
  resolution_note: z
    .enum(["older_than_minute_retention", "older_than_hourly_retention"])
    .nullable(),
});
export type ApiRange = z.infer<typeof apiRange>;

// 7.3 which rows a service's numbers come from
export const apiSourceKind = z.enum(["metric", "span"]);
export const apiSource = z.object({
  service: z.string(),
  source: apiSourceKind,
  span_data_ignored: z.boolean(),
});
export type ApiSource = z.infer<typeof apiSource>;

// 7.4 a percentile in seconds, the open bucket above 10 s, or none without requests
export const percentile = z.union([z.number().nonnegative(), z.literal(">10 s")]).nullable();
export type Percentile = z.infer<typeof percentile>;

const nonneg = z.number().int().nonnegative();
const rate = z.number().min(0).max(1).nullable();

export const apiSummary = z.object({
  requests: nonneg,
  errors_4xx: nonneg,
  errors_5xx: nonneg,
  rate_4xx: rate,
  rate_5xx: rate,
  p50: percentile,
  p95: percentile,
  p99: percentile,
  lossy: z.boolean(),
});
export type ApiSummary = z.infer<typeof apiSummary>;

const apiPeriod = z.object({ range: apiRange, sources: z.array(apiSource), summary: apiSummary });

// 7.5
export const apiOverview = z.object({ current: apiPeriod, previous: apiPeriod });
export type ApiOverview = z.infer<typeof apiOverview>;

// 7.6
export const apiPoint = apiSummary.extend({ at: time });
export type ApiPoint = z.infer<typeof apiPoint>;
export const apiTimeseries = z.object({
  range: apiRange,
  sources: z.array(apiSource),
  points: z.array(apiPoint).max(800),
});
export type ApiTimeseries = z.infer<typeof apiTimeseries>;

// 7.7
export const apiRouteSort = z.enum([
  "requests",
  "errors_4xx",
  "errors_5xx",
  "rate_4xx",
  "rate_5xx",
  "p50",
  "p95",
  "p99",
  "route",
]);
export type ApiRouteSort = z.infer<typeof apiRouteSort>;

export const apiRouteItem = apiSummary.extend({
  service: z.string(),
  method: z.string(),
  route: z.string(),
  source: apiSourceKind,
});
export type ApiRouteItem = z.infer<typeof apiRouteItem>;
export const apiRoutes = z.object({
  range: apiRange,
  sources: z.array(apiSource),
  total: nonneg,
  items: z.array(apiRouteItem),
  next_cursor: z.string().nullable(),
});
export type ApiRoutes = z.infer<typeof apiRoutes>;

// 7.8
export const statusClass = z.enum(["1xx", "2xx", "3xx", "4xx", "5xx", "unknown"]);
export type StatusClass = z.infer<typeof statusClass>;
export const apiStatus = z.object({
  range: apiRange,
  sources: z.array(apiSource),
  requests: nonneg,
  // always these six, in this order
  classes: z.tuple([
    z.object({ class: z.literal("1xx"), requests: nonneg }),
    z.object({ class: z.literal("2xx"), requests: nonneg }),
    z.object({ class: z.literal("3xx"), requests: nonneg }),
    z.object({ class: z.literal("4xx"), requests: nonneg }),
    z.object({ class: z.literal("5xx"), requests: nonneg }),
    z.object({ class: z.literal("unknown"), requests: nonneg }),
  ]),
  codes: z.array(
    z.object({
      status_code: z.number().int().min(0).max(999),
      class: statusClass,
      requests: nonneg,
    }),
  ),
});
export type ApiStatus = z.infer<typeof apiStatus>;

// 7.9
export const apiServiceItem = apiSource.extend({ requests: nonneg, lossy: z.boolean() });
export type ApiServiceItem = z.infer<typeof apiServiceItem>;
export const apiServices = z.object({ range: apiRange, items: z.array(apiServiceItem) });
export type ApiServices = z.infer<typeof apiServices>;

// 7.10
export const warningKind = z.enum([
  "span_derived",
  "lossy_histogram",
  "cardinality_overflow",
  "secret_in_path",
  "coarse_buckets",
]);
export type WarningKind = z.infer<typeof warningKind>;
function warningTotal<K extends WarningKind>(kind: K) {
  return z.object({ kind: z.literal(kind), count: nonneg, last_day: z.iso.date().nullable() });
}
export const apiWarnings = z.object({
  range: apiRange,
  // always these five, in this order
  totals: z.tuple([
    warningTotal("span_derived"),
    warningTotal("lossy_histogram"),
    warningTotal("cardinality_overflow"),
    warningTotal("secret_in_path"),
    warningTotal("coarse_buckets"),
  ]),
  days: z.array(
    z.object({ day: z.iso.date(), kind: warningKind, count: z.number().int().positive() }),
  ),
});
export type ApiWarnings = z.infer<typeof apiWarnings>;

// ---- this stage: global web and api views (contract section 8)

const count = z.number().int().nonnegative();
const share01 = z.number().min(0).max(1).nullable();
const day = z.iso.date();

// 8.2 the project every list row and compare project names
export const tenantRef = z.object({
  id,
  slug: z.string(),
  name: z.string(),
  status: tenantStatus,
  created_at: time,
});
export type TenantRef = z.infer<typeof tenantRef>;

const siteFacts = {
  sites: count,
  sites_verified: count,
  sites_with_stellar_toml: count,
  has_web_data: z.boolean(),
  has_api_data: z.boolean(),
};

export const keyValue = z.object({ key: z.string(), value: count });
export type KeyValue = z.infer<typeof keyValue>;

export const webDimension = z.enum(["referrer", "country", "device", "browser", "os"]);
export type WebDimension = z.infer<typeof webDimension>;

// 8.3 one row of the global web list
export const webProject = z.object({
  tenant: tenantRef,
  ...siteFacts,
  pageviews: count,
  visitors: count,
  visits: count,
  bounces: count,
  totaltime: count,
  bounce_rate: share01,
  avg_visit_duration: z.number().nonnegative().nullable(),
  quarantine_pageviews: count,
  quarantine_visitors: count,
  quarantine_share: share01,
  ingest_requests: count,
  ingest_rejected: count,
  rejected_share: share01,
  top: z.object({
    referrer: z.array(keyValue),
    country: z.array(keyValue),
    device: z.array(keyValue),
    browser: z.array(keyValue),
  }),
});
export type WebProject = z.infer<typeof webProject>;

// 8.2 the list envelope of both project lists
function globalPage<T extends z.ZodType>(item: T) {
  return z.object({
    from: day,
    to: day,
    total: count,
    items: z.array(item),
    next_cursor: z.string().nullable(),
  });
}
export const webProjectPage = globalPage(webProject);
export type WebProjectPage = z.infer<typeof webProjectPage>;

export const webSort = z.enum([
  "name",
  "status",
  "created_at",
  "sites",
  "sites_verified",
  "sites_with_stellar_toml",
  "pageviews",
  "visitors",
  "visits",
  "bounces",
  "totaltime",
  "bounce_rate",
  "avg_visit_duration",
  "quarantine_pageviews",
  "quarantine_visitors",
  "quarantine_share",
  "ingest_requests",
  "ingest_rejected",
  "rejected_share",
]);
export type WebSort = z.infer<typeof webSort>;

// 8.5 a percentile: seconds, or the open bucket above 10 s (seconds null,
// flag true), or none without requests (null and false)
const percentiles = {
  p50_seconds: z.number().nonnegative().nullable(),
  p50_over_10s: z.boolean(),
  p95_seconds: z.number().nonnegative().nullable(),
  p95_over_10s: z.boolean(),
  p99_seconds: z.number().nonnegative().nullable(),
  p99_over_10s: z.boolean(),
};

const apiNumbers = {
  requests: count,
  requests_4xx: count,
  requests_5xx: count,
  rate_4xx: share01,
  rate_5xx: share01,
  ...percentiles,
};
export type ApiNumbers = {
  requests: number;
  requests_4xx: number;
  requests_5xx: number;
  rate_4xx: number | null;
  rate_5xx: number | null;
  p50_seconds: number | null;
  p50_over_10s: boolean;
  p95_seconds: number | null;
  p95_over_10s: boolean;
  p99_seconds: number | null;
  p99_over_10s: boolean;
};

export const projectSource = z.enum(["metric", "span", "mixed"]).nullable();

const apiShape = {
  services: count,
  routes: count,
  span_services: count,
  source: projectSource,
  lossy: z.boolean(),
};

// 8.5 one row of the global api list
export const apiProject = z.object({ tenant: tenantRef, ...siteFacts, ...apiNumbers, ...apiShape });
export type ApiProject = z.infer<typeof apiProject>;
export const apiProjectPage = globalPage(apiProject);
export type ApiProjectPage = z.infer<typeof apiProjectPage>;

export const apiSort = z.enum([
  "name",
  "status",
  "created_at",
  "sites",
  "requests",
  "requests_4xx",
  "requests_5xx",
  "rate_4xx",
  "rate_5xx",
  "p50",
  "p95",
  "p99",
  "services",
  "routes",
]);
export type ApiSort = z.infer<typeof apiSort>;

// 8.4 web compare
const nullableCount = count.nullable();
export const webPoint = z.object({
  day,
  aggregated: z.boolean(),
  pageviews: nullableCount,
  visitors: nullableCount,
  visits: nullableCount,
  bounces: nullableCount,
  totaltime: nullableCount,
  bounce_rate: share01,
  avg_visit_duration: z.number().nonnegative().nullable(),
  quarantine_pageviews: nullableCount,
});
export type WebPoint = z.infer<typeof webPoint>;

export const breakdown = z.object({ items: z.array(keyValue), total_keys: count });
export type Breakdown = z.infer<typeof breakdown>;

export const webCompareProject = z.object({
  tenant: tenantRef,
  totals: z.object({
    pageviews: count,
    visitors: count,
    visits: count,
    bounces: count,
    totaltime: count,
    bounce_rate: share01,
    avg_visit_duration: z.number().nonnegative().nullable(),
    quarantine_pageviews: count,
    quarantine_visitors: count,
    quarantine_share: share01,
  }),
  series: z.array(webPoint),
  breakdowns: z.object({
    referrer: breakdown,
    country: breakdown,
    device: breakdown,
    browser: breakdown,
    os: breakdown,
  }),
});
export type WebCompareProject = z.infer<typeof webCompareProject>;

// every series has one point per day, so the charts align
function aligned<T extends { days: string[]; projects: { series: { day: string }[] }[] }>(
  v: T,
): boolean {
  return v.projects.every(
    (p) => p.series.length === v.days.length && p.series.every((s, i) => s.day === v.days[i]),
  );
}

export const webCompare = z
  .object({
    from: day,
    to: day,
    days: z.array(day),
    projects: z.array(webCompareProject).min(1).max(5),
  })
  .refine(aligned, { message: "series not aligned with days" });
export type WebCompare = z.infer<typeof webCompare>;

// 8.6 api compare
export const apiComparePoint = z.object({ day, ...apiNumbers });
export type ApiComparePoint = z.infer<typeof apiComparePoint>;
export const apiCompareService = z.object({
  service: z.string(),
  source: apiSourceKind,
  ...apiNumbers,
  routes: count,
  lossy: z.boolean(),
});
export type ApiCompareService = z.infer<typeof apiCompareService>;
export const apiCompareProject = z.object({
  tenant: tenantRef,
  totals: z.object({ ...apiNumbers, ...apiShape }),
  series: z.array(apiComparePoint),
  services: z.array(apiCompareService),
});
export type ApiCompareProject = z.infer<typeof apiCompareProject>;
export const apiCompare = z
  .object({
    from: day,
    to: day,
    days: z.array(day),
    projects: z.array(apiCompareProject).min(1).max(5),
  })
  .refine(aligned, { message: "series not aligned with days" });
export type ApiCompare = z.infer<typeof apiCompare>;

// ---- this stage: operator api keys (contract 9.2 and 9.11)

export const apiKeyStatus = z.enum(["active", "expired", "revoked"]);
export type ApiKeyStatus = z.infer<typeof apiKeyStatus>;
export const apiKey = z.object({
  id,
  name: z.string(),
  prefix: z.string().startsWith("sa_op_"),
  scope: z.literal("metrics:read"),
  tenant_id: id.nullable(),
  status: apiKeyStatus,
  created_by: id,
  created_at: time,
  expires_at: time,
  revoked_at: time.nullable(),
  last_used_at: time.nullable(),
});
export type ApiKey = z.infer<typeof apiKey>;
export const apiKeyPage = page(apiKey);

// the design: sa_op_ and 40 base62 characters, shown once
export const apiKeyCreated = apiKey.extend({ key: z.string().regex(/^sa_op_[0-9A-Za-z]{40}$/) });
export type ApiKeyCreated = z.infer<typeof apiKeyCreated>;

export const apiKeyUsage = z.object({
  key_id: id,
  from: day,
  to: day,
  totals: z.object({ requests: count, rejected: count }),
  hours: z.array(z.object({ hour: time, requests: count, rejected: count })),
});
export type ApiKeyUsage = z.infer<typeof apiKeyUsage>;

// 10.1 erasure request. the object never carries the ip address, the user
// agent or the visitor token of a visitor_today request (contract 10.1)
export const erasureKind = z.enum(["visitor_today", "session", "site_reset"]);
export type ErasureKind = z.infer<typeof erasureKind>;
export const erasureStatus = z.enum(["pending", "running", "done", "failed"]);
export type ErasureStatus = z.infer<typeof erasureStatus>;
export const erasureOutcome = z.enum(["erased", "nothing_found"]);
export type ErasureOutcome = z.infer<typeof erasureOutcome>;

const nonnegCount = z.number().int().nonnegative();
export const erasureRequest = z.object({
  id,
  tenant_id: id,
  site_id: id,
  kind: erasureKind,
  status: erasureStatus,
  outcome: erasureOutcome.nullable(),
  message: z.string().nullable(),
  day: z.iso.date().nullable(),
  session_id: id.nullable(),
  requested_by: id,
  requested_at: time,
  completed_at: time.nullable(),
  sessions_deleted: nonnegCount,
  events_deleted: nonnegCount,
  dlq_messages_deleted: nonnegCount,
  affected_from: z.iso.date().nullable(),
  affected_to: z.iso.date().nullable(),
  reaggregated_at: time.nullable(),
  error: z.string().nullable(),
});
export type ErasureRequest = z.infer<typeof erasureRequest>;
export const erasureRequestPage = page(erasureRequest);
export type ErasureRequestPage = z.infer<typeof erasureRequestPage>;

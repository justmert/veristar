// the on-chain witness, the exports and "what scf sees" (control plane
// contract sections 15 and 16): schemas, calls and query options in one
// module, so the shared api files only gain the imports they need

import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { request, requestNoContent } from "./client";
import { comparisonFields } from "./cohort-schemas";
import { cohortRecord, rule, ruleMatch } from "./cohorts";
import { onchainDayTotal, onchainSummary } from "./onchain-schemas";

export {
  type CompareOnchain,
  compareOnchain,
  type OnchainDayTotal,
  type OnchainSummary,
  onchainDayTotal,
  onchainSummary,
} from "./onchain-schemas";

// the same setting as schemas.ts: no parser compiled with new Function,
// which the console's csp refuses. this module can load first.
z.config({ jitless: true });

const enc = encodeURIComponent;
const day = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);
const time = z.string();
const count = z.number().int().nonnegative();

export const chainKind = z.enum(["account", "contract"]);
export type ChainKind = z.infer<typeof chainKind>;

export const verifyMethodChain = z.enum([
  "sep53",
  "manage_data",
  "contract_admin",
  "contract_deployer",
]);
export type ChainVerifyMethod = z.infer<typeof verifyMethodChain>;

export const chainVerification = z.object({
  status: z.enum(["verified", "unverified"]),
  method: verifyMethodChain.nullable(),
  verified_at: time.nullable(),
  via: z.string().nullable(),
  detail: z.record(z.string(), z.unknown()).nullable(),
  last_attempt_at: time.nullable(),
  last_error: z.string().nullable(),
});
export type ChainVerification = z.infer<typeof chainVerification>;

export const chainRead = z.object({
  source: z.enum(["horizon", "rpc_events"]),
  covered_from: day.nullable(),
  covered_to: day.nullable(),
  last_success_at: time.nullable(),
  last_error: z.string().nullable(),
  last_error_at: time.nullable(),
  home_domain: z.string().nullable(),
  account_exists: z.boolean().nullable(),
});

export const chainPoint = z.object({
  day,
  operations: count.nullable(),
  transactions: count.nullable(),
  counterparties: count.nullable(),
  missing: z.string().nullable(),
});
export type ChainPoint = z.infer<typeof chainPoint>;

export const chainId = z.object({
  id: z.string(),
  strkey: z.string(),
  kind: chainKind,
  added_by: z.enum(["enrollment", "console"]),
  created_at: time,
  verification: chainVerification,
  read: chainRead,
  totals: z.object({
    operations: count,
    transactions: count,
    peak_counterparties: count,
    days_read: count,
    days_missing: count,
  }),
  series: z.array(chainPoint),
});
export type ChainId = z.infer<typeof chainId>;

export const linkSite = z.object({
  site_id: z.string(),
  domain: z.string(),
  verified: z.boolean(),
  stellar_toml: z.string().nullable(),
  org_url: z.string().nullable(),
  org_url_matches: z.boolean(),
  accounts: z.array(
    z.object({
      strkey: z.string(),
      listed_in_toml: z.boolean(),
      home_domain: z.string().nullable(),
      home_domain_matches: z.boolean(),
      two_way: z.boolean(),
    }),
  ),
  contracts: z.array(z.object({ strkey: z.string(), listed_in_toml: z.boolean() })),
  two_way: z.boolean(),
});
export type LinkSite = z.infer<typeof linkSite>;

export const onchainView = z.object({
  from: day,
  to: day,
  days: z.array(day),
  sources: z.object({
    accounts: z.string(),
    contracts: z.string(),
    account_backfill_days: count,
  }),
  totals: z.object({
    ids: count,
    ids_verified: count,
    accounts: count,
    contracts: count,
    operations: count,
    transactions: count,
    days_missing: count,
  }),
  ids: z.array(chainId),
  link: z.object({ two_way: z.boolean(), sites: z.array(linkSite) }),
});
export type OnchainView = z.infer<typeof onchainView>;

export const chainIdJSON = chainId.pick({
  id: true,
  strkey: true,
  kind: true,
  added_by: true,
  created_at: true,
  verification: true,
});
export type ChainIdJSON = z.infer<typeof chainIdJSON>;

export const chainChallenge = z.object({
  id: z.string(),
  account: z.string(),
  message: z.string(),
  manage_data: z.object({ name: z.string(), value: z.string() }),
  expires_at: time,
});
export type ChainChallenge = z.infer<typeof chainChallenge>;

const ratio = z.number().nullable();
const nint = count.nullable();

// "what scf sees": the operator rows of the project. integrity is read
// only when the control plane sends it; the console never fills it in
export const scfView = z.object({
  from: day,
  to: day,
  days: z.array(day),
  in_operator_views: z.boolean(),
  tenant: z
    .object({
      id: z.string(),
      slug: z.string(),
      name: z.string(),
      status: z.string(),
      created_at: time,
    })
    .nullable(),
  sites: z
    .object({ sites: count, sites_verified: count, sites_with_stellar_toml: count })
    .nullable(),
  has_web_data: z.boolean(),
  has_api_data: z.boolean(),
  web: z
    .object({
      pageviews: count,
      visitors: count,
      visits: count,
      bounces: count,
      totaltime: count,
      bounce_rate: ratio,
      avg_visit_duration: ratio,
      quarantine_pageviews: count,
      quarantine_visitors: count,
      quarantine_share: ratio,
      ingest_requests: count,
      ingest_rejected: count,
      rejected_share: ratio,
    })
    .nullable(),
  api: z
    .object({
      requests: count,
      requests_4xx: count,
      requests_5xx: count,
      rate_4xx: ratio,
      rate_5xx: ratio,
      p50_seconds: ratio,
      p50_over_10s: z.boolean(),
      p95_seconds: ratio,
      p95_over_10s: z.boolean(),
      p99_seconds: ratio,
      p99_over_10s: z.boolean(),
      services: count,
      routes: count,
      source: z.string().nullable(),
      lossy: z.boolean(),
    })
    .nullable(),
  onchain: onchainSummary.nullable(),
  link_two_way: z.boolean(),
  web_series: z.array(
    z.object({
      day,
      aggregated: z.boolean(),
      pageviews: nint,
      visitors: nint,
      visits: nint,
    }),
  ),
  api_series: z.array(z.object({ day, requests: count })),
  onchain_series: z.array(onchainDayTotal),
  // 17.3 the integrity grade of the project's row
  integrity: z
    .object({
      grade: z.enum(["A", "B", "C"]).nullable(),
      graded_days: count,
      days_a: count,
      days_b: count,
      days_c: count,
    })
    .optional(),
  // 19.3 the project's cohort record, the cohort its percentiles come from
  // (program and round, both null for every listed project), its row's
  // change and percentiles, and the saved rules it meets today
  cohort: cohortRecord,
  percentile_cohort: z
    .object({ program: z.string().nullable(), round: z.string().nullable() })
    .nullable(),
  comparison: z.object(comparisonFields).nullable(),
  rules_met: z.array(z.object({ rule, match: ruleMatch })),
});
export type SCFView = z.infer<typeof scfView>;

export type RangeParams = { from?: string | null; to?: string | null };

function rangeQuery(r: RangeParams): string {
  const q = new URLSearchParams();
  if (r.from) q.set("from", r.from);
  if (r.to) q.set("to", r.to);
  const s = q.toString();
  return s ? `?${s}` : "";
}

export const onchainApi = {
  view: (tenantId: string, r: RangeParams, signal?: AbortSignal) =>
    request(
      "GET",
      `/tenants/${enc(tenantId)}/onchain${rangeQuery(r)}`,
      onchainView,
      undefined,
      signal,
    ),
  add: (tenantId: string, strkey: string) =>
    request("POST", `/tenants/${enc(tenantId)}/onchain/ids`, chainIdJSON, { strkey }),
  remove: (tenantId: string, id: string) =>
    requestNoContent("DELETE", `/tenants/${enc(tenantId)}/onchain/ids/${enc(id)}`),
  challenge: (tenantId: string, id: string) =>
    request(
      "POST",
      `/tenants/${enc(tenantId)}/onchain/ids/${enc(id)}/challenge`,
      chainChallenge,
      {},
    ),
  verify: (
    tenantId: string,
    id: string,
    body: { method: ChainVerifyMethod; signature?: string; tx_hash?: string },
  ) =>
    request("POST", `/tenants/${enc(tenantId)}/onchain/ids/${enc(id)}/verify`, chainIdJSON, body),
  scfView: (tenantId: string, r: RangeParams, signal?: AbortSignal) =>
    request(
      "GET",
      `/tenants/${enc(tenantId)}/scf-view${rangeQuery(r)}`,
      scfView,
      undefined,
      signal,
    ),
  operatorView: (tenantId: string, r: RangeParams, signal?: AbortSignal) =>
    request(
      "GET",
      `/operator/tenants/${enc(tenantId)}/onchain${rangeQuery(r)}`,
      onchainView,
      undefined,
      signal,
    ),
};

export const onchainKeys = {
  // under the tenant prefix, so every tenant invalidation refreshes it
  view: (tenantId: string, r: RangeParams) => ["tenant", tenantId, "onchain", r] as const,
  all: (tenantId: string) => ["tenant", tenantId, "onchain"] as const,
  scf: (tenantId: string, r: RangeParams) => ["tenant", tenantId, "scf-view", r] as const,
  operator: (tenantId: string, r: RangeParams) => ["operator", "onchain", tenantId, r] as const,
};

export const onchainQuery = (tenantId: string, r: RangeParams) =>
  queryOptions({
    queryKey: onchainKeys.view(tenantId, r),
    queryFn: ({ signal }) => onchainApi.view(tenantId, r, signal),
  });

export const scfViewQuery = (tenantId: string, r: RangeParams) =>
  queryOptions({
    queryKey: onchainKeys.scf(tenantId, r),
    queryFn: ({ signal }) => onchainApi.scfView(tenantId, r, signal),
  });

export const operatorOnchainQuery = (tenantId: string, r: RangeParams) =>
  queryOptions({
    queryKey: onchainKeys.operator(tenantId, r),
    queryFn: ({ signal }) => onchainApi.operatorView(tenantId, r, signal),
  });

// csv exports are plain links: the browser sends the session cookie with
// a same-origin get, and the control plane streams the file
export const exportPaths = {
  overview: (tenantId: string, r: RangeParams) =>
    `/api/tenants/${enc(tenantId)}/overview.csv${rangeQuery(r)}`,
  // query is the list's own query string, without cursor and limit
  list: (view: "web" | "api", query: URLSearchParams, rows: number) => {
    const q = new URLSearchParams(query);
    q.delete("cursor");
    q.delete("limit");
    q.set("rows", String(rows));
    return `/api/operator/${view}/projects.csv?${q.toString()}`;
  },
  compare: (view: "web" | "api", tenants: string[], r: RangeParams) => {
    const q = new URLSearchParams();
    if (r.from) q.set("from", r.from);
    if (r.to) q.set("to", r.to);
    q.set("tenants", tenants.join(","));
    return `/api/operator/${view}/compare.csv?${q.toString()}`;
  },
};

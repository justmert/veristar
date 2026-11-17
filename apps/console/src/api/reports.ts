// signed usage reports (control plane contract section 20): schemas, calls
// and query options in one module, like onchain.ts

import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { request } from "./client";

z.config({ jitless: true });

const enc = encodeURIComponent;
const day = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);
const time = z.string();
const count = z.number().int().nonnegative();
const decimal = z
  .string()
  .regex(/^-?\d+\.\d{6}$/)
  .nullable();
const strkey = z.string().regex(/^G[A-Z2-7]{55}$/);

// the signature member of a signed file
export const reportSignature = z.object({
  scheme: z.literal("sep-53"),
  signer: strkey,
  value: z.string().min(1),
});
export type ReportSignature = z.infer<typeof reportSignature>;

// the signed file. the report is kept exactly as received (a record keeps
// every member, an object schema would drop unknown ones), because the
// signature covers its canonical form and nothing may be lost before the
// check
export const signedFile = z.object({
  report: z.record(z.string(), z.unknown()),
  signature: reportSignature,
});
export type SignedFile = z.infer<typeof signedFile>;

// 20.4 a stored report as its project lists it
export const reportSummary = z.object({
  id: z.string().regex(/^[a-z2-7]{26}$/),
  url: z.string(),
  from: day,
  to: day,
  signer: strkey,
  created_at: time,
  created_by: z.uuid().nullable(),
  revoked_at: time.nullable(),
  view_count: count,
  last_viewed_at: time.nullable(),
});
export type ReportSummary = z.infer<typeof reportSummary>;
export const reportList = z.object({ items: z.array(reportSummary) });
export const reportDetail = reportSummary.extend({ file: signedFile });
export type ReportDetail = z.infer<typeof reportDetail>;

// 20.5 the platform's report keys
export const reportKeys = z.object({
  current: strkey,
  retired: z.array(strkey),
  stellar_toml: z.string(),
});
export type ReportKeys = z.infer<typeof reportKeys>;

// 20.5 GET /api/public/reports/{id}
export const publicReport = z.object({
  id: z.string(),
  url: z.string(),
  created_at: time,
  file: signedFile,
  keys: reportKeys,
});
export type PublicReport = z.infer<typeof publicReport>;

// the report document (docs/reference/usage-reports.md), read for display
// only; the signature check runs on the raw object. a stored report keeps
// the layout of the version it was signed in, so every version is read
const kpiDoc = z.object({
  value: decimal,
  previous: decimal,
  change: decimal,
  percentile: decimal,
});
const latency = z.object({ seconds: decimal, over_10s: z.boolean() });
export const reportDocumentV1 = z.object({
  format: z.literal("veristar-usage-report"),
  version: z.literal(1),
  id: z.string(),
  issuer: z.object({
    name: z.string(),
    console: z.string(),
    stellar_toml: z.string(),
    signing_key: strkey,
  }),
  project: z.object({ id: z.string(), slug: z.string(), name: z.string() }),
  period: z.object({ from: day, to: day, days: count }),
  generated_at: time,
  methods: z.object({
    format: count,
    web_metrics: count,
    api_metrics: count,
    onchain: count,
    cohort: count,
    integrity_rules: count,
    route_normaliser: count,
    platform: z.string(),
  }),
  web: z.object({
    pageviews: count,
    visitors: count,
    visits: count,
    bounces: count,
    totaltime_seconds: count,
    bounce_rate: decimal,
    avg_visit_duration_seconds: decimal,
    quarantine_pageviews: count,
    quarantine_visitors: count,
    quarantine_share: decimal,
    gateway_requests: count,
    gateway_rejected: count,
    sites: count,
    sites_verified: count,
    series: z.array(
      z.object({
        day,
        aggregated: z.boolean(),
        pageviews: count.nullable(),
        visitors: count.nullable(),
        visits: count.nullable(),
      }),
    ),
  }),
  api: z.object({
    requests: count,
    requests_4xx: count,
    requests_5xx: count,
    rate_4xx: decimal,
    rate_5xx: decimal,
    p50: latency,
    p95: latency,
    p99: latency,
    services: count,
    routes: count,
    source: z.string().nullable(),
    series: z.array(z.object({ day, requests: count, requests_4xx: count, requests_5xx: count })),
  }),
  onchain: z.object({
    ids: count,
    ids_verified: count,
    operations: count,
    transactions: count,
    days_missing: count,
    two_way_link: z.boolean(),
    series: z.array(
      z.object({
        day,
        ids_read: count,
        operations: count.nullable(),
        transactions: count.nullable(),
      }),
    ),
  }),
  integrity: z.object({
    grade: z.enum(["A", "B", "C"]).nullable(),
    graded_days: count,
    days_a: count,
    days_b: count,
    days_c: count,
    reasons: z.array(
      z.object({ code: z.string(), level: z.string(), days: count, text: z.string() }),
    ),
  }),
  cohort: z.object({
    program: z.string().nullable(),
    round: z.string().nullable(),
    category: z.string().nullable(),
    award_date: day.nullable(),
    percentile_program: z.string().nullable(),
    percentile_round: z.string().nullable(),
    cohort_size: count,
    kpis: z.record(z.string(), kpiDoc),
  }),
  rules_met: z.array(
    z.object({
      name: z.string(),
      metric: z.string(),
      min_per_day: count,
      min_days: count,
      months: count,
      min_grade: z.string().nullable(),
      from: z.string(),
      to: z.string(),
    }),
  ),
});

// version 2: the integrity rule versions that graded the period, the
// coverage estimate, has_data, lossy, infrastructure requests and weekly
// active accounts
const v1 = reportDocumentV1.shape;
export const reportDocumentV2 = reportDocumentV1.extend({
  version: z.literal(2),
  methods: v1.methods.extend({ integrity_rules: z.array(z.number().int().positive()) }),
  web: v1.web.extend({
    has_data: z.boolean(),
    coverage: z.object({
      script_requests: count,
      gpc_requests: count,
      unmeasured_share: decimal,
    }),
  }),
  api: v1.api.extend({
    has_data: z.boolean(),
    lossy: z.boolean(),
    infrastructure_requests: count,
  }),
  onchain: v1.onchain.extend({
    weekly_active_accounts: z.object({
      average: decimal,
      latest: count.nullable(),
      weeks_counted: count,
      weeks_missing: count,
    }),
  }),
});

export const reportDocument = z.discriminatedUnion("version", [reportDocumentV1, reportDocumentV2]);
export type ReportDocument = z.infer<typeof reportDocument>;

export const reportApi = {
  list: (tenantId: string, signal?: AbortSignal) =>
    request("GET", `/tenants/${enc(tenantId)}/reports`, reportList, undefined, signal),
  create: (tenantId: string, body: { from: string; to: string }) =>
    request("POST", `/tenants/${enc(tenantId)}/reports`, reportDetail, body),
  get: (tenantId: string, reportId: string, signal?: AbortSignal) =>
    request(
      "GET",
      `/tenants/${enc(tenantId)}/reports/${enc(reportId)}`,
      reportDetail,
      undefined,
      signal,
    ),
  revoke: (tenantId: string, reportId: string) =>
    request("POST", `/tenants/${enc(tenantId)}/reports/${enc(reportId)}/revoke`, reportSummary),
  public: (reportId: string, signal?: AbortSignal) =>
    request("GET", `/public/reports/${enc(reportId)}`, publicReport, undefined, signal),
};

export const reportKeysOf = {
  // under the tenant prefix, so every tenant invalidation refreshes it
  list: (tenantId: string) => ["tenant", tenantId, "reports"] as const,
  public: (reportId: string) => ["public-report", reportId] as const,
};

export const reportsQuery = (tenantId: string) =>
  queryOptions({
    queryKey: reportKeysOf.list(tenantId),
    queryFn: ({ signal }) => reportApi.list(tenantId, signal),
  });

export const publicReportQuery = (reportId: string) =>
  queryOptions({
    queryKey: reportKeysOf.public(reportId),
    queryFn: ({ signal }) => reportApi.public(reportId, signal),
    // every read is a counted, audited view: never read again by itself
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
    retry: false,
  });

// the signed file as people download it: the report and its signature,
// nothing else
export function fileJSON(file: SignedFile): string {
  return `${JSON.stringify({ report: file.report, signature: file.signature }, null, 2)}\n`;
}

// the web dashboard (control plane contract section 18): schemas, calls and
// query options in one module, so the shared api files only gain the
// imports they need

import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { request } from "./client";

// the same setting as schemas.ts: no parser compiled with new Function,
// which the console's csp refuses. this module can load first.
z.config({ jitless: true });

const enc = encodeURIComponent;
const day = z.string().regex(/^\d{4}-\d{2}-\d{2}$/);
const count = z.number().int().nonnegative();
const ratio = z.number().nonnegative().nullable();

export const webBand = z.enum(["primary", "quarantine"]);
export type WebBand = z.infer<typeof webBand>;

const siteRef = z.object({
  id: z.string(),
  name: z.string(),
  domain: z.string(),
  status: z.string(),
});
export type WebSiteRef = z.infer<typeof siteRef>;

const range = z.object({ from: day, to: day, days: z.number().int().positive() });

export const webTotals = z.object({
  pageviews: count,
  visitors: count,
  visits: count,
  bounces: count,
  totaltime: count,
  bounce_rate: ratio,
  avg_visit_duration: ratio,
  aggregated_days: count,
});
export type WebTotals = z.infer<typeof webTotals>;

export const webPoint = z.object({
  day,
  aggregated: z.boolean(),
  pageviews: count.nullable(),
  visitors: count.nullable(),
  visits: count.nullable(),
  bounces: count.nullable(),
  totaltime: count.nullable(),
  bounce_rate: ratio,
  avg_visit_duration: ratio,
});
export type WebPoint = z.infer<typeof webPoint>;

const period = z.object({ range, totals: webTotals, series: z.array(webPoint) });
export type WebPeriod = z.infer<typeof period>;

export const webSummary = z.object({
  band: webBand,
  site: siteRef.nullable(),
  sites: z.array(siteRef),
  current: period,
  previous: period,
  other_band: z.object({ band: webBand, pageviews: count, visitors: count }),
  quarantine_share: ratio,
  read_at: z.string().nullable(),
  source: z.literal("aggregate"),
});
export type WebSummary = z.infer<typeof webSummary>;

export const WEB_DIMENSIONS = [
  "page",
  "entry_page",
  "exit_page",
  "referrer",
  "country",
  "browser",
  "os",
  "device",
  "event",
  "utm_source",
  "utm_medium",
  "utm_campaign",
  "utm_content",
  "utm_term",
] as const;
export const webDimension = z.enum(WEB_DIMENSIONS);
export type WebDimension = z.infer<typeof webDimension>;

export const webBreakdown = z.object({
  dimension: webDimension,
  band: webBand,
  range,
  source: z.enum(["aggregate", "engine"]),
  unit: z.enum(["visitors", "events"]),
  items: z.array(z.object({ key: z.string(), value: count, site_id: z.string().optional() })),
  total_keys: count,
  sites: z.array(siteRef).nullable(),
});
export type WebBreakdown = z.infer<typeof webBreakdown>;

const bucketPoint = z.object({ at: z.string(), pageviews: count, visitors: count });
export type WebBucket = z.infer<typeof bucketPoint>;

export const webHourly = z.object({
  day,
  band: webBand,
  source: z.literal("engine"),
  sites: z.array(siteRef),
  points: z.array(bucketPoint),
});
export type WebHourly = z.infer<typeof webHourly>;

export const webRealtime = z.object({
  at: z.string(),
  source: z.literal("engine"),
  visitors: count,
  sites: z.array(z.object({ site: siteRef, visitors: count })),
  minutes: z.array(bucketPoint),
});
export type WebRealtime = z.infer<typeof webRealtime>;

// what the dashboard asks for: utc days, both included, an optional site
// and the band
export type WebQuery = { from: string; to: string; site: string | null; band: WebBand };

function query(pairs: [string, string | null | undefined][]): string {
  const q = new URLSearchParams();
  for (const [k, v] of pairs) if (v) q.set(k, v);
  const s = q.toString();
  return s ? `?${s}` : "";
}

const scope = (q: WebQuery): [string, string | null][] => [
  ["from", q.from],
  ["to", q.to],
  ["site", q.site],
  ["band", q.band === "primary" ? null : q.band],
];

export const webApi = {
  summary: (tenantId: string, q: WebQuery, signal?: AbortSignal) =>
    request(
      "GET",
      `/tenants/${enc(tenantId)}/web/summary${query(scope(q))}`,
      webSummary,
      undefined,
      signal,
    ),
  breakdown: (
    tenantId: string,
    q: WebQuery,
    dimension: WebDimension,
    limit: number,
    signal?: AbortSignal,
  ) =>
    request(
      "GET",
      `/tenants/${enc(tenantId)}/web/breakdown${query([["dimension", dimension], ...scope(q), ["limit", String(limit)]])}`,
      webBreakdown,
      undefined,
      signal,
    ),
  hourly: (tenantId: string, d: string, site: string | null, band: WebBand, signal?: AbortSignal) =>
    request(
      "GET",
      `/tenants/${enc(tenantId)}/web/hourly${query([
        ["day", d],
        ["site", site],
        ["band", band === "primary" ? null : band],
      ])}`,
      webHourly,
      undefined,
      signal,
    ),
  realtime: (tenantId: string, site: string | null, signal?: AbortSignal) =>
    request(
      "GET",
      `/tenants/${enc(tenantId)}/web/realtime${query([["site", site]])}`,
      webRealtime,
      undefined,
      signal,
    ),
};

// under the tenant prefix, so every tenant invalidation refreshes them
export const webKeys = {
  all: (tenantId: string) => ["tenant", tenantId, "web"] as const,
  summary: (tenantId: string, q: WebQuery) => ["tenant", tenantId, "web", "summary", q] as const,
  breakdown: (tenantId: string, q: WebQuery, d: WebDimension, limit: number) =>
    ["tenant", tenantId, "web", "breakdown", q, d, limit] as const,
  hourly: (tenantId: string, d: string, site: string | null, band: WebBand) =>
    ["tenant", tenantId, "web", "hourly", d, site, band] as const,
  realtime: (tenantId: string, site: string | null) =>
    ["tenant", tenantId, "web", "realtime", site] as const,
};

// the aggregates are read every hour; a minute keeps a page switch cheap
const aggStale = 60_000;

export const webSummaryQuery = (tenantId: string, q: WebQuery) =>
  queryOptions({
    queryKey: webKeys.summary(tenantId, q),
    queryFn: ({ signal }) => webApi.summary(tenantId, q, signal),
    staleTime: aggStale,
    placeholderData: keepPreviousData,
  });

export const webBreakdownQuery = (tenantId: string, q: WebQuery, d: WebDimension, limit: number) =>
  queryOptions({
    queryKey: webKeys.breakdown(tenantId, q, d, limit),
    queryFn: ({ signal }) => webApi.breakdown(tenantId, q, d, limit, signal),
    staleTime: aggStale,
    placeholderData: keepPreviousData,
  });

export const webHourlyQuery = (tenantId: string, d: string, site: string | null, band: WebBand) =>
  queryOptions({
    queryKey: webKeys.hourly(tenantId, d, site, band),
    queryFn: ({ signal }) => webApi.hourly(tenantId, d, site, band, signal),
    staleTime: aggStale,
  });

// realtime polls every 30 seconds while the page is visible
export const REALTIME_POLL = 30_000;

export const webRealtimeQuery = (tenantId: string, site: string | null) =>
  queryOptions({
    queryKey: webKeys.realtime(tenantId, site),
    queryFn: ({ signal }) => webApi.realtime(tenantId, site, signal),
    refetchInterval: REALTIME_POLL,
    staleTime: REALTIME_POLL / 2,
  });

export const webSessions = z.object({
  day,
  band: webBand,
  site: siteRef,
  source: z.literal("engine"),
  page: z.number().int().positive(),
  page_size: z.number().int().positive(),
  total: count,
  items: z.array(
    z.object({
      id: z.string(),
      first_at: z.string(),
      last_at: z.string(),
      visits: count,
      views: count,
      country: z.string(),
      browser: z.string(),
      os: z.string(),
      device: z.string(),
    }),
  ),
});
export type WebSessions = z.infer<typeof webSessions>;

// one site's sessions of one utc day, for the erasure of one session
export const webSessionsQuery = (
  tenantId: string,
  site: string,
  d: string,
  page: number,
  band: WebBand = "primary",
) =>
  queryOptions({
    queryKey: ["tenant", tenantId, "web", "sessions", site, d, band, page] as const,
    queryFn: ({ signal }) =>
      request(
        "GET",
        `/tenants/${enc(tenantId)}/web/sessions${query([
          ["site", site],
          ["day", d],
          ["band", band === "primary" ? null : band],
          ["page", page > 1 ? String(page) : null],
        ])}`,
        webSessions,
        undefined,
        signal,
      ),
    placeholderData: keepPreviousData,
  });

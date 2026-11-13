// query keys and options in one place, so a mutation invalidates exactly what
// it changed

import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import type { DayRange, ListState } from "@/lib/global";
import {
  type ApiFilters,
  type ApiRangeQuery,
  type ApiRoutesQuery,
  type AuditQuery,
  api,
  globalApi,
  type TenantListQuery,
} from "./endpoints";
import type { ApiSort, WebSort } from "./schemas";

export const keys = {
  me: ["me"] as const,
  terms: ["terms"] as const,
  tenant: (id: string) => ["tenant", id] as const,
  members: (id: string) => ["tenant", id, "members"] as const,
  sites: (id: string) => ["tenant", id, "sites"] as const,
  erasure: (id: string) => ["tenant", id, "erasure"] as const,
  erasureRequest: (id: string, requestId: string) =>
    ["tenant", id, "erasure", "one", requestId] as const,
  snippet: (siteId: string, version: string | null) => ["snippet", siteId, version] as const,
  ingest: (siteId: string, window: string) => ["ingest", siteId, window] as const,
  verification: (siteId: string) => ["verification", siteId] as const,
  otlpTokens: (id: string, includeRevoked: boolean) =>
    ["tenant", id, "otlp-tokens", includeRevoked] as const,
  otlpTokensAll: (id: string) => ["tenant", id, "otlp-tokens"] as const,
  apiMetrics: (id: string, kind: string, ...rest: unknown[]) =>
    ["tenant", id, "api-metrics", kind, ...rest] as const,
  // everything an operator action can change lives under this prefix
  operator: ["operator"] as const,
  opTenants: (q: TenantListQuery) => ["operator", "tenants", q] as const,
  opTenant: (id: string) => ["operator", "tenant", id] as const,
  opMode: ["operator", "settings", "enrollment-mode"] as const,
  opKeep: ["operator", "settings", "removal-keep-aggregates"] as const,
  opAudit: (q: AuditQuery) => ["operator", "audit", q] as const,
  opGlobal: (kind: string, ...rest: unknown[]) => ["operator", "global", kind, ...rest] as const,
  opKeys: ["operator", "api-keys"] as const,
  opKeyUsage: (id: string) => ["operator", "api-keys", id, "usage"] as const,
  opKeyTenants: ["operator", "key-tenants"] as const,
  opAlerts: ["operator", "alerts"] as const,
};

export const meQuery = queryOptions({
  queryKey: keys.me,
  queryFn: ({ signal }) => api.me(signal),
  staleTime: 30_000,
});

export const termsQuery = queryOptions({
  queryKey: keys.terms,
  queryFn: ({ signal }) => api.terms(signal),
  staleTime: 5 * 60_000,
});

export const tenantQuery = (id: string) =>
  queryOptions({ queryKey: keys.tenant(id), queryFn: ({ signal }) => api.tenant(id, signal) });

export const membersQuery = (id: string) =>
  queryOptions({ queryKey: keys.members(id), queryFn: ({ signal }) => api.members(id, signal) });

export const sitesQuery = (id: string) =>
  queryOptions({ queryKey: keys.sites(id), queryFn: ({ signal }) => api.sites(id, signal) });

// erasure runs finish in the control plane after the 202; while one is
// running the history is read again every few seconds so its result shows
// without a reload
const erasurePoll = 2_000;

export const erasureListQuery = (id: string) =>
  infiniteQueryOptions({
    queryKey: keys.erasure(id),
    queryFn: ({ signal, pageParam }) => api.erasureRequests(id, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    refetchInterval: (q) =>
      q.state.data?.pages.some((p) => p.items.some((r) => r.status === "running"))
        ? erasurePoll
        : false,
  });

export const erasureRequestQuery = (id: string, requestId: string) =>
  queryOptions({
    queryKey: keys.erasureRequest(id, requestId),
    queryFn: ({ signal }) => api.erasureRequest(id, requestId, signal),
    refetchInterval: (q) => (q.state.data?.status === "running" ? erasurePoll : false),
  });

export const snippetQuery = (siteId: string, version: string | null) =>
  queryOptions({
    queryKey: keys.snippet(siteId, version),
    queryFn: ({ signal }) => api.snippet(siteId, version, signal),
    staleTime: 5 * 60_000,
  });

// last 24 full hours: the route's default window (contract 4.3.7)
export const ingestDayQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.ingest(siteId, "24h"),
    queryFn: ({ signal }) => api.ingest(siteId, null, signal),
    // rows are folded every few minutes; refetching more often shows nothing new
    staleTime: 2 * 60_000,
  });

// last 30 days in day buckets, ending at the current whole utc hour (the
// route accepts only whole hours)
export function thirtyDayRange(now: Date): { from: string; to: string; bucket: "day" } {
  const to = new Date(now);
  to.setUTCMinutes(0, 0, 0);
  const from = new Date(to.getTime() - 30 * 24 * 3600_000);
  const iso = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");
  return { from: iso(from), to: iso(to), bucket: "day" };
}

export const ingestMonthQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.ingest(siteId, "30d"),
    queryFn: ({ signal }) => api.ingest(siteId, thirtyDayRange(new Date()), signal),
    staleTime: 5 * 60_000,
  });

export const otlpTokensQuery = (id: string, includeRevoked: boolean) =>
  queryOptions({
    queryKey: keys.otlpTokens(id, includeRevoked),
    queryFn: ({ signal }) => api.otlpTokens(id, includeRevoked, signal),
  });

export const verificationQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.verification(siteId),
    queryFn: ({ signal }) => api.siteVerification(siteId, signal),
  });

export const operatorTenantsQuery = (q: TenantListQuery) =>
  infiniteQueryOptions({
    queryKey: keys.opTenants(q),
    queryFn: ({ signal, pageParam }) => api.operator.tenants(q, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });

export const operatorTenantQuery = (id: string) =>
  queryOptions({
    queryKey: keys.opTenant(id),
    queryFn: ({ signal }) => api.operator.tenant(id, signal),
  });

export const enrollmentModeQuery = queryOptions({
  queryKey: keys.opMode,
  queryFn: ({ signal }) => api.operator.enrollmentMode(signal),
});

export const keepAggregatesQuery = queryOptions({
  queryKey: keys.opKeep,
  queryFn: ({ signal }) => api.operator.keepAggregates(signal),
});

export const auditQuery = (q: AuditQuery) =>
  infiniteQueryOptions({
    queryKey: keys.opAudit(q),
    queryFn: ({ signal, pageParam }) => api.operator.audit(q, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });

// the alerts page, the nav badge and the critical banner share this query,
// so the operator area asks the control plane once per interval. polling
// goes on in a background tab: an alert that fires while the tab is hidden
// is on screen when the operator comes back, not 30 seconds later.
export const ALERTS_REFRESH_MS = 30_000;

export const operatorAlertsQuery = queryOptions({
  queryKey: keys.opAlerts,
  queryFn: ({ signal }) => api.operator.alerts(signal),
  refetchInterval: ALERTS_REFRESH_MS,
  refetchIntervalInBackground: true,
  staleTime: 0,
});

// rollups run every 5 minutes and minute rows arrive as batches are written,
// so a minute is the useful refresh; earlier data stays on screen meanwhile
const apiStale = 60_000;

// a new range or filter keeps the previous answer on screen until the new
// one arrives (no layout jump), but never the answer of another project
function sameTenant(id: string) {
  return <T>(prev: T | undefined, prevQuery: { queryKey: readonly unknown[] } | undefined) =>
    prevQuery?.queryKey[1] === id ? prev : undefined;
}

export const apiOverviewQuery = (id: string, r: ApiRangeQuery, f: ApiFilters) =>
  queryOptions({
    queryKey: keys.apiMetrics(id, "overview", r, f),
    queryFn: ({ signal }) => api.apiMetrics.overview(id, r, f, signal),
    staleTime: apiStale,
    placeholderData: sameTenant(id),
  });

export const apiTimeseriesQuery = (id: string, r: ApiRangeQuery, f: ApiFilters) =>
  queryOptions({
    queryKey: keys.apiMetrics(id, "timeseries", r, f),
    queryFn: ({ signal }) => api.apiMetrics.timeseries(id, r, f, signal),
    staleTime: apiStale,
    placeholderData: sameTenant(id),
  });

export const apiStatusQuery = (id: string, r: ApiRangeQuery, f: ApiFilters) =>
  queryOptions({
    queryKey: keys.apiMetrics(id, "status", r, f),
    queryFn: ({ signal }) => api.apiMetrics.status(id, r, f, signal),
    staleTime: apiStale,
    placeholderData: sameTenant(id),
  });

export const apiRoutesQuery = (id: string, r: ApiRangeQuery, f: ApiFilters, q: ApiRoutesQuery) =>
  queryOptions({
    queryKey: keys.apiMetrics(id, "routes", r, f, q),
    queryFn: ({ signal }) => api.apiMetrics.routes(id, r, f, q, signal),
    staleTime: apiStale,
    placeholderData: sameTenant(id),
  });

export const apiServicesQuery = (id: string, r: ApiRangeQuery) =>
  queryOptions({
    queryKey: keys.apiMetrics(id, "services", r),
    queryFn: ({ signal }) => api.apiMetrics.services(id, r, signal),
    staleTime: apiStale,
    placeholderData: sameTenant(id),
  });

export const apiWarningsQuery = (id: string, r: ApiRangeQuery) =>
  queryOptions({
    queryKey: keys.apiMetrics(id, "warnings", r),
    queryFn: ({ signal }) => api.apiMetrics.warnings(id, r, signal),
    staleTime: apiStale,
    placeholderData: sameTenant(id),
  });

// the aggregation task reads umami every hour and the rollup runs every 5
// minutes, so a minute is the useful refresh for the global views
const globalStale = 60_000;

export const webProjectsQuery = (s: ListState<WebSort>) =>
  infiniteQueryOptions({
    queryKey: keys.opGlobal("web-projects", s),
    queryFn: ({ signal, pageParam }) => globalApi.webProjects(s, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    staleTime: globalStale,
    placeholderData: (prev) => prev,
  });

export const apiProjectsQuery = (s: ListState<ApiSort>) =>
  infiniteQueryOptions({
    queryKey: keys.opGlobal("api-projects", s),
    queryFn: ({ signal, pageParam }) => globalApi.apiProjects(s, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
    staleTime: globalStale,
    placeholderData: (prev) => prev,
  });

export const webCompareQuery = (tenants: string[], r: DayRange) =>
  queryOptions({
    queryKey: keys.opGlobal("web-compare", tenants, r),
    queryFn: ({ signal }) => globalApi.webCompare(tenants, r, signal),
    staleTime: globalStale,
  });

export const apiCompareQuery = (tenants: string[], r: DayRange) =>
  queryOptions({
    queryKey: keys.opGlobal("api-compare", tenants, r),
    queryFn: ({ signal }) => globalApi.apiCompare(tenants, r, signal),
    staleTime: globalStale,
  });

export const apiKeysQuery = queryOptions({
  queryKey: keys.opKeys,
  queryFn: ({ signal }) => globalApi.apiKeys(signal),
});

export const apiKeyUsageQuery = (id: string) =>
  queryOptions({
    queryKey: keys.opKeyUsage(id),
    queryFn: ({ signal }) => globalApi.apiKeyUsage(id, signal),
  });

export const keyTenantsQuery = queryOptions({
  queryKey: keys.opKeyTenants,
  queryFn: ({ signal }) => globalApi.keyTenants(signal),
  staleTime: 5 * 60_000,
});

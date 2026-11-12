// query keys and options in one place, so a mutation invalidates exactly what
// it changed

import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { type AuditQuery, api, type TenantListQuery } from "./endpoints";

export const keys = {
  me: ["me"] as const,
  terms: ["terms"] as const,
  tenant: (id: string) => ["tenant", id] as const,
  members: (id: string) => ["tenant", id, "members"] as const,
  sites: (id: string) => ["tenant", id, "sites"] as const,
  snippet: (siteId: string, version: string | null) => ["snippet", siteId, version] as const,
  ingest: (siteId: string, window: string) => ["ingest", siteId, window] as const,
  verification: (siteId: string) => ["verification", siteId] as const,
  otlpTokens: (id: string, includeRevoked: boolean) =>
    ["tenant", id, "otlp-tokens", includeRevoked] as const,
  otlpTokensAll: (id: string) => ["tenant", id, "otlp-tokens"] as const,
  // everything an operator action can change lives under this prefix
  operator: ["operator"] as const,
  opTenants: (q: TenantListQuery) => ["operator", "tenants", q] as const,
  opTenant: (id: string) => ["operator", "tenant", id] as const,
  opMode: ["operator", "settings", "enrollment-mode"] as const,
  opKeep: ["operator", "settings", "removal-keep-aggregates"] as const,
  opAudit: (q: AuditQuery) => ["operator", "audit", q] as const,
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

// one function per control plane route the console uses (contract sections 4
// and 5)

import { request, requestNoContent } from "./client";
import {
  addMemberResult,
  auditPage,
  type EnrollmentMode,
  enrollmentMode,
  ingest,
  keepAggregates,
  me,
  member,
  members,
  operatorSite,
  operatorTenantDetail,
  operatorTenantPage,
  operatorTenantSummary,
  type Role,
  type Site,
  site,
  sitePage,
  siteVerification,
  snippet,
  type TenantAction,
  type TenantStatus,
  tenant,
  terms,
  type VerifyMethod,
} from "./schemas";

const enc = encodeURIComponent;

export type EnrollmentInput = {
  name: string;
  slug?: string;
  description?: string;
  enrollment: {
    website: string;
    stellar_usage: string;
    stellar_ids?: string[];
    scf_round_url?: string;
  };
  accepted_terms_version: string;
};

export type SiteInput = { name: string; domain: string; origins: string[] };
export type SitePatch = { name?: string; origins?: string[] };

// 5.7 resubmission of a rejected enrollment
export type ResubmitInput = {
  name?: string;
  description?: string;
  enrollment: EnrollmentInput["enrollment"];
};

// 5.3 query of the operator tenant list
export type TenantListQuery = {
  status: TenantStatus[];
  review: boolean;
  q: string;
  sort: "created_at" | "status_changed_at" | "name";
  order: "asc" | "desc";
};

// 5.9 audit filters; empty strings are left out of the query
export type AuditQuery = {
  tenant_id: string;
  target: string;
  action: string;
  actor: string;
  from: string;
  to: string;
};

function query(params: [string, string][]): string {
  const kept = params.filter(([, v]) => v !== "");
  return kept.length ? `?${kept.map(([k, v]) => `${k}=${enc(v)}`).join("&")}` : "";
}

export function tenantListPath(q: TenantListQuery, cursor: string | null): string {
  return `/operator/tenants${query([
    ["status", q.status.join(",")],
    ["review", q.review ? "true" : ""],
    ["q", q.q.trim()],
    ["sort", q.sort],
    ["order", q.order],
    ["limit", "50"],
    ["cursor", cursor ?? ""],
  ])}`;
}

export function auditPath(q: AuditQuery, cursor: string | null): string {
  return `/operator/audit${query([
    ["tenant_id", q.tenant_id.trim()],
    ["target", q.target.trim()],
    ["action", q.action.trim()],
    ["actor", q.actor.trim()],
    ["from", q.from],
    ["to", q.to],
    ["limit", "50"],
    ["cursor", cursor ?? ""],
  ])}`;
}

// a reason is sent only when one was written: approve and resume take an
// optional note, and an empty string would fail the 1 character minimum
function reasonBody(reason: string): { reason?: string } {
  const r = reason.trim();
  return r ? { reason: r } : {};
}

export const api = {
  me: (signal?: AbortSignal) => request("GET", "/me", me, undefined, signal),
  terms: (signal?: AbortSignal) => request("GET", "/terms", terms, undefined, signal),
  logout: () => requestNoContent("POST", "/auth/logout"),

  tenant: (tenantId: string, signal?: AbortSignal) =>
    request("GET", `/tenants/${enc(tenantId)}`, tenant, undefined, signal),
  createTenant: (body: EnrollmentInput) => request("POST", "/tenants", tenant, body),
  acceptTerms: (tenantId: string, version: string) =>
    request("POST", `/tenants/${enc(tenantId)}/terms`, tenant, { version }),
  resubmit: (tenantId: string, body: ResubmitInput) =>
    request("PUT", `/tenants/${enc(tenantId)}/enrollment`, tenant, body),

  members: (tenantId: string, signal?: AbortSignal) =>
    request("GET", `/tenants/${enc(tenantId)}/members`, members, undefined, signal),
  addMember: (tenantId: string, body: { github_login: string; role: Role }) =>
    request("POST", `/tenants/${enc(tenantId)}/members`, addMemberResult, body),
  setMemberRole: (tenantId: string, userId: string, role: Role) =>
    request("PATCH", `/tenants/${enc(tenantId)}/members/${enc(userId)}`, member, { role }),
  removeMember: (tenantId: string, userId: string) =>
    requestNoContent("DELETE", `/tenants/${enc(tenantId)}/members/${enc(userId)}`),
  revokeInvite: (tenantId: string, inviteId: string) =>
    requestNoContent("DELETE", `/tenants/${enc(tenantId)}/invites/${enc(inviteId)}`),

  // follows next_cursor, so a long list is never cut at the first page
  sites: async (tenantId: string, signal?: AbortSignal): Promise<Site[]> => {
    const out: Site[] = [];
    let cursor: string | null = null;
    do {
      const q: string = cursor ? `&cursor=${enc(cursor)}` : "";
      const p = await request(
        "GET",
        `/tenants/${enc(tenantId)}/sites?limit=200${q}`,
        sitePage,
        undefined,
        signal,
      );
      out.push(...p.items);
      cursor = p.next_cursor;
    } while (cursor !== null);
    return out;
  },
  site: (tenantId: string, siteId: string, signal?: AbortSignal) =>
    request("GET", `/tenants/${enc(tenantId)}/sites/${enc(siteId)}`, site, undefined, signal),
  createSite: (tenantId: string, body: SiteInput) =>
    request("POST", `/tenants/${enc(tenantId)}/sites`, site, body),
  updateSite: (tenantId: string, siteId: string, body: SitePatch) =>
    request("PATCH", `/tenants/${enc(tenantId)}/sites/${enc(siteId)}`, site, body),
  deleteSite: (tenantId: string, siteId: string) =>
    request("DELETE", `/tenants/${enc(tenantId)}/sites/${enc(siteId)}`, site),

  snippet: (siteId: string, version: string | null, signal?: AbortSignal) =>
    request(
      "GET",
      `/sites/${enc(siteId)}/snippet${version ? `?version=${enc(version)}` : ""}`,
      snippet,
      undefined,
      signal,
    ),
  ingest: (
    siteId: string,
    range: { from: string; to: string; bucket: "hour" | "day" } | null,
    signal?: AbortSignal,
  ) => {
    const q = range ? `?from=${enc(range.from)}&to=${enc(range.to)}&bucket=${range.bucket}` : "";
    return request("GET", `/sites/${enc(siteId)}/ingest${q}`, ingest, undefined, signal);
  },

  siteVerification: (siteId: string, signal?: AbortSignal) =>
    request("GET", `/sites/${enc(siteId)}/verification`, siteVerification, undefined, signal),
  verifySite: (siteId: string, method: VerifyMethod | null) =>
    request("POST", `/sites/${enc(siteId)}/verify`, siteVerification, method ? { method } : {}),

  operator: {
    tenants: (q: TenantListQuery, cursor: string | null, signal?: AbortSignal) =>
      request("GET", tenantListPath(q, cursor), operatorTenantPage, undefined, signal),
    tenant: (tenantId: string, signal?: AbortSignal) =>
      request("GET", `/operator/tenants/${enc(tenantId)}`, operatorTenantDetail, undefined, signal),
    tenantAction: (tenantId: string, action: TenantAction, reason: string) =>
      request(
        "POST",
        `/operator/tenants/${enc(tenantId)}/${action}`,
        operatorTenantSummary,
        reasonBody(reason),
      ),
    siteAction: (siteId: string, action: "approve" | "reject", reason: string) =>
      request("POST", `/operator/sites/${enc(siteId)}/${action}`, operatorSite, reasonBody(reason)),
    enrollmentMode: (signal?: AbortSignal) =>
      request("GET", "/operator/settings/enrollment-mode", enrollmentMode, undefined, signal),
    setEnrollmentMode: (mode: EnrollmentMode) =>
      request("PUT", "/operator/settings/enrollment-mode", enrollmentMode, { mode }),
    keepAggregates: (signal?: AbortSignal) =>
      request(
        "GET",
        "/operator/settings/removal-keep-aggregates",
        keepAggregates,
        undefined,
        signal,
      ),
    setKeepAggregates: (keep: boolean) =>
      request("PUT", "/operator/settings/removal-keep-aggregates", keepAggregates, { keep }),
    audit: (q: AuditQuery, cursor: string | null, signal?: AbortSignal) =>
      request("GET", auditPath(q, cursor), auditPage, undefined, signal),
  },
};

// browser navigations, not fetch calls (contract 4.1.1 and 4.4.1)
export function signInUrl(returnTo: string): string {
  return `/api/auth/github/start?return_to=${enc(returnTo)}`;
}

export function webDashboardUrl(siteId: string, view: "primary" | "quarantine"): string {
  return `/api/sso/umami?site=${enc(siteId)}${view === "quarantine" ? "&view=quarantine" : ""}`;
}

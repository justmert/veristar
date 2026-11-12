// one function per control plane route the console uses (contract section 4)

import { request, requestNoContent } from "./client";
import {
  addMemberResult,
  ingest,
  me,
  member,
  members,
  type Role,
  type Site,
  site,
  sitePage,
  snippet,
  tenant,
  terms,
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

export const api = {
  me: (signal?: AbortSignal) => request("GET", "/me", me, undefined, signal),
  terms: (signal?: AbortSignal) => request("GET", "/terms", terms, undefined, signal),
  logout: () => requestNoContent("POST", "/auth/logout"),

  tenant: (tenantId: string, signal?: AbortSignal) =>
    request("GET", `/tenants/${enc(tenantId)}`, tenant, undefined, signal),
  createTenant: (body: EnrollmentInput) => request("POST", "/tenants", tenant, body),
  acceptTerms: (tenantId: string, version: string) =>
    request("POST", `/tenants/${enc(tenantId)}/terms`, tenant, { version }),

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
};

// browser navigations, not fetch calls (contract 4.1.1 and 4.4.1)
export function signInUrl(returnTo: string): string {
  return `/api/auth/github/start?return_to=${enc(returnTo)}`;
}

export function webDashboardUrl(siteId: string, view: "primary" | "quarantine"): string {
  return `/api/sso/umami?site=${enc(siteId)}${view === "quarantine" ? "&view=quarantine" : ""}`;
}

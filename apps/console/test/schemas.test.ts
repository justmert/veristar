import { describe, expect, it } from "vitest";
import {
  addMemberResult,
  auditPage,
  enrollmentMode,
  errorEnvelope,
  ingest,
  keepAggregates,
  me,
  members,
  operatorSite,
  operatorTenantDetail,
  operatorTenantPage,
  operatorTenantSummary,
  site,
  sitePage,
  siteVerification,
  snippet,
  stellarToml,
  tenant,
  terms,
} from "@/api/schemas";
import * as f from "./fixtures/contract";

describe("schemas accept the contract's response shapes", () => {
  it.each([
    ["me (4.1.4)", me, f.meBody],
    ["terms (4.1.5)", terms, f.termsBody],
    ["tenant (3.1)", tenant, f.tenantBody],
    ["members (4.2.5)", members, f.membersBody],
    ["add member, member (4.2.5)", addMemberResult, f.addMemberAsMemberBody],
    ["add member, invite (4.2.5)", addMemberResult, f.addMemberAsInviteBody],
    ["site (3.1)", site, f.siteBody],
    ["site list (2.3)", sitePage, f.sitePageBody],
    ["snippet (4.3.6)", snippet, f.snippetBody],
    ["ingest (4.3.7)", ingest, f.ingestBody],
    ["error envelope (2.2)", errorEnvelope, f.validationErrorBody],
    ["me with removed projects (5.10)", me, f.meWithRemovedBody],
    ["site with this stage fields (5.10)", site, f.siteVerifiedBody],
    ["stellar.toml found (5.12)", stellarToml, f.stellarTomlFound],
    ["stellar.toml not found (5.12)", stellarToml, f.stellarTomlNotFound],
    ["operator tenant summary (5.2)", operatorTenantSummary, f.operatorSummaryBody],
    ["operator tenant list (5.3)", operatorTenantPage, f.operatorSitePageBody],
    ["operator site (5.2)", operatorSite, f.operatorSiteBody],
    ["operator tenant detail (5.4)", operatorTenantDetail, f.operatorDetailBody],
    ["operator tenant detail, removed (5.4)", operatorTenantDetail, f.removedDetailBody],
    ["audit list (5.9)", auditPage, f.auditPageBody],
    ["enrollment mode (5.7)", enrollmentMode, { mode: "approval" }],
    ["removal keep aggregates (5.13)", keepAggregates, { keep: false }],
    ["site verification (5.8)", siteVerification, f.verificationBody],
  ])("%s", (_name, schema, body) => {
    const r = schema.safeParse(body);
    expect(r.success, JSON.stringify(r.error?.issues)).toBe(true);
  });

  it("keeps nullable columns as null (contract 1)", () => {
    const t = tenant.parse({ ...f.tenantBody, accepted_terms_version: null });
    expect(t.accepted_terms_version).toBeNull();
    const s = site.parse({ ...f.siteBody, quarantine_website_id: null });
    expect(s.quarantine_website_id).toBeNull();
  });

  it("accepts fields a later phase adds", () => {
    const s = site.parse({ ...f.siteBody, added_in_phase_9: 1 });
    expect(s.id).toBe(f.SITE_ID);
  });

  it("refuses a this stage response with a missing or unknown value", () => {
    const { in_review: _, ...noReview } = f.siteBody;
    expect(site.safeParse(noReview).success).toBe(false);
    expect(enrollmentMode.safeParse({ mode: "closed" }).success).toBe(false);
    expect(
      operatorTenantDetail.safeParse({ ...f.operatorDetailBody, allowed_actions: ["delete"] })
        .success,
    ).toBe(false);
    expect(me.safeParse({ ...f.meBody, removed_tenants: undefined }).success).toBe(false);
  });

  it("parses the npm object once the tracker is published", () => {
    const npm = {
      package: "tracker",
      version: "1.0.0",
      install: "pnpm add tracker@1.0.0",
      import: 'import "tracker";',
    };
    expect(snippet.parse({ ...f.snippetBody, npm }).npm).toEqual(npm);
  });

  it("passes unknown ingest outcomes through", () => {
    const r = ingest.parse({ ...f.ingestBody, totals: { accepted: 1, rejected_future_reason: 4 } });
    expect(r.totals.rejected_future_reason).toBe(4);
  });
});

describe("schemas refuse responses that break the contract", () => {
  it.each([
    ["tenant without role", tenant, { ...f.tenantBody, role: undefined }],
    ["tenant with an unknown status", tenant, { ...f.tenantBody, status: "deleted" }],
    ["tenant with an omitted nullable", tenant, { ...f.tenantBody, status_reason: undefined }],
    ["site with an id that is not a uuid", site, { ...f.siteBody, id: "site-1" }],
    ["site with a local time", site, { ...f.siteBody, created_at: "2026-11-12 09:00" }],
    ["snippet without versions", snippet, { ...f.snippetBody, versions: [] }],
    ["snippet with a sha256 integrity", snippet, { ...f.snippetBody, integrity: "sha256-abc" }],
    ["ingest with a negative count", ingest, { ...f.ingestBody, totals: { accepted: -1 } }],
    ["add member with an unknown kind", addMemberResult, { kind: "user", member: f.memberBody }],
    ["me with a string github id", me, { ...f.meBody, user: { ...f.meBody.user, github_id: "1" } }],
  ])("%s", (_name, schema, body) => {
    expect(schema.safeParse(body).success).toBe(false);
  });
});

import { describe, expect, it } from "vitest";
import { ApiError, ResponseShapeError } from "@/api/client";
import { api } from "@/api/endpoints";
import * as f from "./fixtures/contract";
import { stubFetch } from "./helpers";

describe("api client", () => {
  it("sends reads without the csrf header and same-origin credentials", async () => {
    const calls = stubFetch([{ status: 200, body: f.meBody }]);
    const me = await api.me();
    expect(me.user.github_login).toBe("octocat");
    expect(calls[0]?.url).toBe("/api/me");
    expect(calls[0]?.method).toBe("GET");
    expect(calls[0]?.headers.get("X-Requested-With")).toBeNull();
  });

  it("sends X-Requested-With: sa-console on every state change (contract 1.2)", async () => {
    const calls = stubFetch([
      { status: 201, body: f.siteBody },
      { status: 200, body: f.siteBody },
      { status: 202, body: { ...f.siteBody, status: "removed" } },
      { status: 204 },
      { status: 204 },
    ]);
    await api.createSite(f.TENANT_ID, {
      name: "a",
      domain: "example.org",
      origins: ["https://example.org"],
    });
    await api.updateSite(f.TENANT_ID, f.SITE_ID, { name: "b" });
    const removed = await api.deleteSite(f.TENANT_ID, f.SITE_ID);
    await api.removeMember(f.TENANT_ID, f.USER_ID);
    await api.logout();
    expect(removed.status).toBe("removed");
    expect(calls.map((c) => c.method)).toEqual(["POST", "PATCH", "DELETE", "DELETE", "POST"]);
    for (const c of calls) expect(c.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[0]?.headers.get("Content-Type")).toBe("application/json; charset=utf-8");
    expect(calls[1]?.body).toEqual({ name: "b" });
  });

  it("turns the error envelope into an ApiError with fields and request id", async () => {
    stubFetch([{ status: 422, body: f.validationErrorBody }]);
    const err = await api
      .createSite(f.TENANT_ID, { name: "a", domain: "example.org", origins: [] })
      .catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("validation_failed");
    expect(err.fields).toEqual({ "origins[1]": "not_bare_origin" });
    expect(err.requestId).toBe(f.validationErrorBody.error.request_id);
  });

  it("reads Retry-After on 429 and 503", async () => {
    stubFetch([
      { status: 429, body: f.errorBody("rate_limited"), headers: { "Retry-After": "30" } },
    ]);
    const err = await api.me().catch((e) => e);
    expect(err.code).toBe("rate_limited");
    expect(err.retryAfter).toBe(30);
  });

  it("reports a proxy error without an envelope with its status", async () => {
    stubFetch([{ status: 502 }]);
    const err = await api.me().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(502);
    expect(err.code).toBe("unavailable");
  });

  it("refuses a 200 whose body breaks the contract", async () => {
    stubFetch([{ status: 200, body: { ...f.tenantBody, provisioning: "done" } }]);
    const err = await api.tenant(f.TENANT_ID).catch((e) => e);
    expect(err).toBeInstanceOf(ResponseShapeError);
    expect(err.message).toContain("provisioning");
  });

  it("follows next_cursor until the last page of sites", async () => {
    const second = { ...f.siteBody, id: f.QUARANTINE_ID, quarantine_website_id: null };
    const calls = stubFetch([
      { status: 200, body: { items: [f.siteBody], next_cursor: "abc" } },
      { status: 200, body: { items: [second], next_cursor: null } },
    ]);
    const sites = await api.sites(f.TENANT_ID);
    expect(sites.map((s) => s.id)).toEqual([f.SITE_ID, f.QUARANTINE_ID]);
    expect(calls[1]?.url).toContain("cursor=abc");
  });

  it("asks for the 30-day window in whole utc hours, the running hour included", async () => {
    const { thirtyDayRange } = await import("@/api/queries");
    const r = thirtyDayRange(new Date("2026-11-12T09:41:12.345Z"));
    expect(r).toEqual({ from: "2026-10-13T10:00:00Z", to: "2026-11-12T10:00:00Z", bucket: "day" });
  });
});

import { describe, expect, it } from "vitest";
import type { Me, Site, Tenant } from "@/api/schemas";
import { RemovedNotices } from "@/pages/home/projects";
import { VerifySection } from "@/pages/project/site-verify";
import { RemovedNotice, StatusNotice } from "@/pages/project-layout";
import { resubmitBody, resubmitValues } from "@/pages/resubmit";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, render, renderRoutes, stubFetch, type, waitFor } from "./helpers";

const base = f.tenantBody as Tenant;

describe("tenant status banner (contract 5.10)", () => {
  it("shows nothing for an active, set up project", async () => {
    const host = await render(<StatusNotice tenant={base} isOwner />);
    expect(host.textContent).toBe("");
  });

  it("says a pending project waits for review", async () => {
    const host = await render(<StatusNotice tenant={{ ...base, status: "pending" }} isOwner />);
    expect(host.textContent).toContain("Waiting for review");
  });

  it("shows the operator's reason for a suspension", async () => {
    const host = await render(
      <StatusNotice
        tenant={{ ...base, status: "suspended", status_reason: "Spam traffic" }}
        isOwner={false}
      />,
    );
    expect(host.textContent).toContain("This project is suspended");
    expect(host.textContent).toContain("Reason given: Spam traffic");
  });

  it("lets an owner of a rejected project edit and resubmit", async () => {
    const rejected: Tenant = { ...base, status: "rejected", status_reason: "Tell us more" };
    const calls = stubFetch([
      { status: 200, body: { ...f.tenantBody, status: "pending" } },
      { status: 200, body: f.meBody },
    ]);
    const host = await render(<StatusNotice tenant={rejected} isOwner />);
    expect(host.textContent).toContain("Reason given: Tell us more");
    await click(byRole(host, "button", "Edit and resubmit"));
    await type(byLabel(host, "How does the project use Stellar?"), "Soroban AMM with 2,000 users");
    await click(byRole(host, "button", "Submit for review"));
    await waitFor(() => expect(calls.length).toBeGreaterThan(0));
    expect(calls[0]?.method).toBe("PUT");
    expect(calls[0]?.url).toBe(`/api/tenants/${f.TENANT_ID}/enrollment`);
    expect(calls[0]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[0]?.body).toEqual({
      enrollment: {
        website: "https://example.org",
        stellar_usage: "Soroban AMM with 2,000 users",
        stellar_ids: [f.G_KEY, f.C_KEY],
      },
    });
  });

  it("sends name and description only when they changed", () => {
    const v = { ...resubmitValues(base), name: "Stellar DEX 2" };
    expect(resubmitBody(base, v)).toEqual({
      name: "Stellar DEX 2",
      enrollment: {
        website: "https://example.org",
        stellar_usage: "Soroban AMM on mainnet",
        stellar_ids: [f.G_KEY, f.C_KEY],
      },
    });
  });

  it("tells a member of a rejected project to ask an owner", async () => {
    const host = await render(
      <StatusNotice tenant={{ ...base, status: "rejected" }} isOwner={false} />,
    );
    expect(host.textContent).toContain("An owner of this project can change the answers");
  });

  it("shows a removed project with its reason", async () => {
    const host = await render(<RemovedNotice name="Old Project" reason="Not a Stellar project" />);
    expect(host.textContent).toContain("removed by an operator");
    expect(host.textContent).toContain("Reason given: Not a Stellar project");
  });

  it("lists removed projects from /api/me until dismissed", async () => {
    const me = f.meWithRemovedBody as Me;
    const host = await render(<RemovedNotices removed={me.removed_tenants} />);
    expect(host.textContent).toContain("Old Project was removed by an operator");
    expect(host.textContent).toContain("Not a Stellar project");
    await click(byRole(host, "button", "Dismiss"));
    expect(host.textContent).toBe("");
  });
});

describe("domain verification, the third setup step (contract 5.8)", () => {
  const routes = [
    {
      path: "/projects/:tenantId/sites/:siteId",
      element: <VerifySection site={f.siteBody as Site} />,
    },
  ];
  const path = `/projects/${f.TENANT_ID}/sites/${f.SITE_ID}`;

  // the step reads the site's verification; the frame around it is the
  // setup page's
  const frame = (verification: unknown) => [{ status: 200, body: verification }];

  it("shows both methods with the token and the last error", async () => {
    stubFetch(frame(f.verificationBody));
    const host = await renderRoutes(routes, path);
    await waitFor(() => expect(host.textContent).toContain("_veristar.example.org"));
    expect(host.textContent).toContain(f.verificationBody.dns.value);
    expect(host.textContent).toContain("https://example.org/.well-known/veristar.txt");
    expect(host.textContent).toContain("No TXT record was found");
    expect(host.textContent).toContain("Domain not verified");
  });

  it("runs the chosen check and reports the result", async () => {
    const calls = stubFetch([
      ...frame(f.verificationBody),
      {
        status: 200,
        body: {
          ...f.verificationBody,
          verification: "verified",
          verify_method: "file",
          verified_at: "2026-11-12T10:00:00Z",
          last_verify_at: "2026-11-12T10:00:00Z",
          last_verify_error: null,
        },
      },
      { status: 200, body: f.sitePageBody },
    ]);
    const host = await renderRoutes(routes, path);
    await waitFor(() => byRole(host, "button", "Check now"));
    const select = byLabel(host, "Check") as unknown as HTMLSelectElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
    setter?.call(select, "file");
    select.dispatchEvent(new Event("change", { bubbles: true }));
    await click(byRole(host, "button", "Check now"));
    await waitFor(() => expect(host.textContent).toContain("Verified."));
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toBe(`/api/sites/${f.SITE_ID}/verify`);
    expect(post?.body).toEqual({ method: "file" });
  });

  it("explains a rate limit", async () => {
    stubFetch([
      ...frame(f.verificationBody),
      { status: 429, body: f.errorBody("rate_limited"), headers: { "Retry-After": "600" } },
    ]);
    const host = await renderRoutes(routes, path);
    await waitFor(() => byRole(host, "button", "Check now"));
    await click(byRole(host, "button", "Check now"));
    await waitFor(() => expect(host.textContent).toContain("Try again in 10 minutes"));
  });
});

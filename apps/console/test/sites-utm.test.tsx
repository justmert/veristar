import { describe, expect, it } from "vitest";
import { type Site, site } from "@/api/schemas";
import { CampaignParams } from "@/pages/project/site-settings";
import * as f from "./fixtures/contract";
import { click, render, stubFetch, waitFor } from "./helpers";

const siteUrl = `/api/tenants/${f.TENANT_ID}/sites/${f.SITE_ID}`;

function box(host: HTMLElement): HTMLElement {
  const el = host.querySelector<HTMLElement>("[role=checkbox]");
  if (!el) throw new Error("no checkbox");
  return el;
}

describe("utm collection setting (contract 3.1 collect_utm, 4.3.4)", () => {
  it("is part of the site and off in the fixture", () => {
    expect(site.parse(f.siteBody).collect_utm).toBe(false);
    expect(() => site.parse({ ...f.siteBody, collect_utm: undefined })).toThrow();
  });

  it("shows off and turns it on with a patch of that field only", async () => {
    const calls = stubFetch([{ status: 200, body: { ...f.siteBody, collect_utm: true } }]);
    const host = await render(<CampaignParams site={f.siteBody as Site} canEdit isOwner />);
    expect(box(host).getAttribute("aria-checked")).toBe("false");
    expect(host.textContent).toContain("Off by default");
    await click(box(host));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]?.method).toBe("PATCH");
    expect(calls[0]?.url).toBe(siteUrl);
    expect(calls[0]?.body).toEqual({ collect_utm: true });
  });

  it("turns it off again", async () => {
    const calls = stubFetch([{ status: 200, body: f.siteBody }]);
    const on = { ...f.siteBody, collect_utm: true } as Site;
    const host = await render(<CampaignParams site={on} canEdit isOwner />);
    expect(box(host).getAttribute("aria-checked")).toBe("true");
    await click(box(host));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]?.body).toEqual({ collect_utm: false });
  });

  it("cannot be changed without edit rights", async () => {
    const calls = stubFetch([]);
    const host = await render(<CampaignParams site={f.siteBody as Site} canEdit={false} isOwner />);
    expect(box(host).hasAttribute("disabled")).toBe(true);
    await click(box(host));
    expect(calls).toHaveLength(0);
  });

  it("is the owner's decision: a member sees it but cannot change it", async () => {
    const calls = stubFetch([]);
    const host = await render(<CampaignParams site={f.siteBody as Site} canEdit isOwner={false} />);
    expect(box(host).hasAttribute("disabled")).toBe(true);
    expect(host.textContent).toContain("Only an owner can change it.");
    await click(box(host));
    expect(calls).toHaveLength(0);
  });
});

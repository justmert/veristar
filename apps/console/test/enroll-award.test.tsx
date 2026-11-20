import { describe, expect, it, vi } from "vitest";
import { awardQuery } from "@/pages/home/award-search";
import { EnrollForm, sameCohort, withAward } from "@/pages/home/enroll";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, render, routeFetch, type, waitFor } from "./helpers";

const AWARD = {
  id: "0193a6f8-1111-7caa-bdbb-eccddeeff001",
  project: "Example Pay",
  round: "SCF #44",
  category: "Applications",
  program: "Build Award",
  award_date: "2026-06-01",
  website: "https://examplepay.example",
  stellar_ids: [f.G_KEY],
  rounds: ["SCF #31"],
};

const COHORT = {
  program: "Build Award",
  round: "SCF #44",
  category: "Applications",
  award_date: "2026-06-01",
};

const BASE = {
  name: "Example Pay",
  enrollment: { website: "https://examplepay.example", stellar_usage: "payments" },
  accepted_terms_version: f.TERMS_VERSION,
};

describe("the award search query", () => {
  it("collapses white space and keeps 2 to 100 characters", () => {
    expect(awardQuery("  ex   pay ")).toBe("ex pay");
    expect(awardQuery("e")).toBeNull();
    expect(awardQuery("x".repeat(101))).toBeNull();
  });
});

describe("the enrollment body with a picked award", () => {
  it("names the award instead of the cohort while the cohort is the award's", () => {
    const picked = { id: AWARD.id, cohort: COHORT };
    expect(withAward(BASE, COHORT, picked)).toEqual({ ...BASE, scf_award_id: AWARD.id });
    // an edited cohort field drops the award and sends the cohort as typed
    const edited = { ...COHORT, round: "SCF #45" };
    expect(sameCohort(edited, COHORT)).toBe(false);
    const body = withAward(BASE, edited, picked);
    expect(body).not.toHaveProperty("scf_award_id");
    expect(body.cohort).toEqual({ ...COHORT, round: "SCF #45" });
  });
});

describe("Find your SCF award", () => {
  it("searches after typing, fills the form from the pick and sends the award id", async () => {
    const calls = routeFetch((method, url) => {
      if (url.startsWith("/api/scf/awards/search"))
        return { status: 200, body: { items: [AWARD] } };
      if (method === "POST")
        return { status: 201, body: { ...f.tenantBody, provisioning: "pending" } };
      return { status: 404, body: f.errorBody("not_found") };
    });
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />, "/projects/new");
    await type(byLabel(host, "Find your SCF award"), "examp");
    await waitFor(() => expect(host.querySelector(`[data-award="${AWARD.id}"]`)).not.toBeNull());
    expect(calls.filter((c) => c.url.startsWith("/api/scf/awards"))).toHaveLength(1);
    expect(calls[0]?.url).toBe("/api/scf/awards/search?q=examp");
    await click(host.querySelector(`[data-award="${AWARD.id}"]`) as HTMLElement);
    expect(byLabel(host, "Name").value).toBe("Example Pay");
    expect(byLabel(host, "Website").value).toBe("https://examplepay.example");
    expect(byLabel(host, "Account or contract IDs").value).toBe(f.G_KEY);
    expect(host.querySelector("[data-award-picked]")?.textContent).toContain("SCF #44");
    await type(byLabel(host, "How the project uses Stellar"), "payments");
    await click(byLabel(host, "I accept the") as unknown as HTMLElement);
    await click(byRole(host, "button", "Enroll project"));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const sent = calls.find((c) => c.method === "POST")?.body as Record<string, unknown>;
    expect(sent.scf_award_id).toBe(AWARD.id);
    expect(sent).not.toHaveProperty("cohort");
  });

  it("shows an empty list without a paragraph", async () => {
    routeFetch((_method, url) => {
      if (url.startsWith("/api/scf/awards/search")) return { status: 200, body: { items: [] } };
      return { status: 404, body: f.errorBody("not_found") };
    });
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />);
    await type(byLabel(host, "Find your SCF award"), "nothing");
    await waitFor(() => expect(host.querySelector("[data-award-state=empty]")).not.toBeNull());
    expect(host.querySelector("[data-award-search] p")).toBeNull();
  });

  it("says on the search when the server no longer knows the picked award", async () => {
    routeFetch((method, url) => {
      if (url.startsWith("/api/scf/awards/search"))
        return { status: 200, body: { items: [AWARD] } };
      if (method === "POST")
        return {
          status: 422,
          body: f.errorBody("validation_failed", { scf_award_id: "unknown_scf_award" }),
        };
      return { status: 404, body: f.errorBody("not_found") };
    });
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />);
    await type(byLabel(host, "Find your SCF award"), "examp");
    await waitFor(() => expect(host.querySelector(`[data-award="${AWARD.id}"]`)).not.toBeNull());
    await click(host.querySelector(`[data-award="${AWARD.id}"]`) as HTMLElement);
    await type(byLabel(host, "How the project uses Stellar"), "payments");
    await click(byLabel(host, "I accept the") as unknown as HTMLElement);
    await click(byRole(host, "button", "Enroll project"));
    await waitFor(() => expect(host.querySelector("[data-award-error]")).not.toBeNull());
    expect(host.querySelector("[data-award-error]")?.textContent).toContain("Search again");
  });

  it("waits out a rate limit instead of asking again", async () => {
    let n = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        n += 1;
        return new Response(JSON.stringify(f.errorBody("rate_limited")), {
          status: 429,
          headers: { "Content-Type": "application/json; charset=utf-8", "Retry-After": "30" },
        });
      }),
    );
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />);
    await type(byLabel(host, "Find your SCF award"), "examp");
    await waitFor(() => expect(host.querySelector("[data-award-state=error]")).not.toBeNull());
    expect(host.querySelector("[data-award-state=error]")?.textContent).toContain("30 seconds");
    await type(byLabel(host, "Find your SCF award"), "exampl");
    await new Promise((r) => setTimeout(r, 400));
    expect(n).toBe(1);
  });
});

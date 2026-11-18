import { Outlet } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { Site } from "@/api/schemas";
import { TooltipProvider } from "@/components/ui/tooltip";
import { apiRangeOfDays, overviewDays, readOverviewPreset } from "@/lib/overview";
import { combineFirstEvent, firstEvent, nextSetupStep } from "@/lib/site-setup";
import { ingestWindow, sparkOf, sumOutcomes } from "@/lib/web-dashboard";
import { OverviewPage } from "@/pages/project/overview";
import type { ProjectContext } from "@/pages/project-layout";
import * as f from "./fixtures/contract";
import { integrityReportBody } from "./fixtures/global";
import { renderRoutes, waitFor } from "./helpers";

const NOW = new Date("2026-11-16T15:04:05Z");
const site = (over: Partial<Site> = {}): Site => ({ ...(f.siteBody as Site), ...over });

describe("overview range", () => {
  it("reads the preset and asks the api for exactly the same utc days", () => {
    expect(readOverviewPreset(null)).toBe("30");
    expect(readOverviewPreset("7")).toBe("7");
    expect(readOverviewPreset("custom")).toBe("30");
    expect(overviewDays("today", NOW)).toEqual({ from: "2026-11-16", to: "2026-11-16" });
    expect(apiRangeOfDays({ from: "2026-11-10", to: "2026-11-16" })).toEqual({
      range: "custom",
      from: "2026-11-10T00:00:00Z",
      to: "2026-11-17T00:00:00Z",
    });
  });
});

describe("next setup step", () => {
  it("completes the first event on either source", () => {
    const edge = (totals: Record<string, number>) =>
      firstEvent({ ...f.ingestBody, totals } as never);
    expect(combineFirstEvent(null, null)).toBeNull();
    expect(combineFirstEvent(edge({}), null)).toBe("waiting");
    expect(combineFirstEvent(edge({ rejected_origin: 3 }), null)).toBe("refused");
    // the web totals saw a page view the edge counts do not hold yet
    expect(combineFirstEvent(edge({}), true)).toBe("received");
    expect(combineFirstEvent(null, true)).toBe("received");
    expect(combineFirstEvent(edge({ quarantined: 1 }), false)).toBe("received");
  });

  it("names one step of the first site that is not set up", () => {
    const a = site({ id: "a", verification: "verified" });
    const b = site({ id: "b", verification: "unchecked" });
    const review = site({ id: "r", in_review: true });
    const events = { a: "received", b: "received", r: "waiting" } as const;
    const step = nextSetupStep([review, a, b], (s) => events[s.id as keyof typeof events]);
    expect(step).toMatchObject({ step: "verify", done: 2 });
    expect(step?.site.id).toBe("b");
    expect(nextSetupStep([b], () => "waiting")).toMatchObject({ step: "install", done: 1 });
    expect(nextSetupStep([b], () => "refused")?.step).toBe("refused");
    expect(nextSetupStep([a], () => "received")).toBeNull();
    // an answer still missing names no step yet, so nothing jumps
    expect(nextSetupStep([b], () => null)).toBeNull();
  });
});

describe("web helpers of the overview and web", () => {
  it("reads outcome counts up to the current whole hour", () => {
    expect(ingestWindow({ from: "2026-11-10", to: "2026-11-16" }, NOW)).toEqual({
      from: "2026-11-10T00:00:00Z",
      to: "2026-11-16T16:00:00Z",
      bucket: "day",
    });
    expect(ingestWindow({ from: "2026-11-01", to: "2026-11-02" }, NOW)?.to).toBe(
      "2026-11-03T00:00:00Z",
    );
    expect(
      ingestWindow({ from: "2026-11-16", to: "2026-11-16" }, new Date("2026-11-16T00:20:00Z")),
    ).toEqual({ from: "2026-11-16T00:00:00Z", to: "2026-11-16T01:00:00Z", bucket: "day" });
    expect(ingestWindow({ from: "2026-11-17", to: "2026-11-17" }, NOW)).toBeNull();
    expect(
      sumOutcomes([{ totals: { accepted: 2 } }, undefined, { totals: { accepted: 3, bot: 1 } }]),
    ).toEqual({
      accepted: 5,
      bot: 1,
    });
  });

  it("draws a sparkline only for a shape", () => {
    const p = (day: string, pv: number | null) => ({
      day,
      aggregated: pv !== null,
      pageviews: pv,
      visitors: pv,
      visits: pv,
      bounces: pv,
      totaltime: pv,
      bounce_rate: null,
      avg_visit_duration: null,
    });
    expect(sparkOf([p("2026-11-15", 3)], "pageviews")).toBeNull();
    expect(sparkOf([p("2026-11-15", 3), p("2026-11-16", 3)], "pageviews")).toBeNull();
    // one or two days with data is a spike, not a shape
    expect(sparkOf([p("2026-11-15", 3), p("2026-11-16", null)], "pageviews")).toBeNull();
    expect(
      sparkOf(
        [p("2026-11-13", 1), p("2026-11-14", 2), p("2026-11-15", 3), p("2026-11-16", null)],
        "pageviews",
      ),
    ).toEqual([1, 2, 3, 0]);
  });
});

// ---- the page

const day = (d: string) => ({
  day: d,
  aggregated: true,
  pageviews: 10,
  visitors: 4,
  visits: 5,
  bounces: 2,
  totaltime: 100,
  bounce_rate: 0.4,
  avg_visit_duration: 20,
});
const totals = (pv: number) => ({
  pageviews: pv,
  visitors: 40,
  visits: 50,
  bounces: 20,
  totaltime: 1000,
  bounce_rate: 0.4,
  avg_visit_duration: 20,
  aggregated_days: 2,
});
const summaryBody = (pv: number) => ({
  band: "primary",
  site: null,
  sites: [],
  current: {
    range: { from: "2026-11-15", to: "2026-11-16", days: 2 },
    totals: totals(pv),
    series: [day("2026-11-15"), day("2026-11-16")],
  },
  previous: {
    range: { from: "2026-11-13", to: "2026-11-14", days: 2 },
    totals: totals(80),
    series: [day("2026-11-13"), day("2026-11-14")],
  },
  other_band: { band: "quarantine", pageviews: 3, visitors: 1 },
  quarantine_share: 0.03,
  read_at: "2026-11-16T15:00:00Z",
  source: "aggregate",
});
const onchainBody = {
  from: "2026-11-15",
  to: "2026-11-16",
  days: ["2026-11-15", "2026-11-16"],
  sources: { accounts: "horizon", contracts: "rpc_events", account_backfill_days: 90 },
  totals: {
    ids: 2,
    ids_verified: 1,
    accounts: 1,
    contracts: 1,
    operations: 1234,
    transactions: 5,
    days_missing: 0,
    weekly_active_accounts: { average: null, latest: null, weeks_counted: 0, weeks_missing: 1 },
  },
  ids: [
    chainId("01a1ed00-0000-7000-8000-000000000001", "verified", [1200, 34]),
    // an unverified id's operations stay out of the card
    chainId("01a1ed00-0000-7000-8000-000000000002", "unverified", [500, 500]),
  ],
  weeks: [],
  link: { two_way: false, sites: [] },
};

function chainId(id: string, status: "verified" | "unverified", ops: [number, number]) {
  return {
    id,
    strkey: f.G_KEY,
    kind: "account",
    added_by: "enrollment",
    created_at: "2026-11-12T09:00:00Z",
    verification: {
      status,
      method: status === "verified" ? "sep53" : null,
      verified_at: status === "verified" ? "2026-11-12T10:00:00Z" : null,
      via: null,
      detail: null,
      last_attempt_at: null,
      last_error: null,
    },
    read: {
      source: "horizon",
      covered_from: "2026-11-15",
      covered_to: "2026-11-16",
      last_success_at: "2026-11-16T01:00:00Z",
      last_error: null,
      last_error_at: null,
      home_domain: null,
      account_exists: true,
    },
    totals: {
      operations: ops[0] + ops[1],
      transactions: 2,
      peak_counterparties: 1,
      days_read: 2,
      days_missing: 0,
    },
    series: [
      { day: "2026-11-15", operations: ops[0], transactions: 1, counterparties: 1, missing: null },
      { day: "2026-11-16", operations: ops[1], transactions: 1, counterparties: 1, missing: null },
    ],
    functions: null,
  };
}

const ctx: ProjectContext = {
  tenant: f.tenantBody as ProjectContext["tenant"],
  me: f.meBody as ProjectContext["me"],
  isOwner: true,
  termsCurrent: true,
};

function stub(opts: { pageviews: number; ingest: Record<string, number>; api: "used" | "none" }) {
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      urls.push(input);
      const u = new URL(input, "https://app.example.org");
      const json = (body: unknown) =>
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { "Content-Type": "application/json; charset=utf-8" },
        });
      if (u.pathname.endsWith("/sites")) return json(f.sitePageBody);
      if (u.pathname.endsWith("/web/summary")) return json(summaryBody(opts.pageviews));
      if (u.pathname.endsWith("/web/hourly"))
        return json({
          day: "2026-11-16",
          band: "primary",
          source: "engine",
          sites: [],
          points: [],
        });
      if (u.pathname.endsWith("/ingest")) return json({ ...f.ingestBody, totals: opts.ingest });
      if (u.pathname.endsWith("/api-metrics/overview"))
        return json(
          opts.api === "used"
            ? f.apiOverviewBody
            : {
                ...f.apiOverviewBody,
                current: {
                  ...f.apiOverviewBody.current,
                  summary: f.apiEmptySummaryBody,
                  sources: [],
                },
                previous: {
                  ...f.apiOverviewBody.previous,
                  summary: f.apiEmptySummaryBody,
                  sources: [],
                },
              },
        );
      if (u.pathname.endsWith("/integrity")) return json(integrityReportBody);
      if (u.pathname.endsWith("/onchain")) return json(onchainBody);
      return new Response(JSON.stringify({ error: { code: "unavailable", message: "x" } }), {
        status: 503,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }),
  );
  return urls;
}

const routes = [
  {
    path: "/projects/:tenantId",
    element: (
      <TooltipProvider>
        <Outlet context={ctx} />
      </TooltipProvider>
    ),
    children: [{ index: true, element: <OverviewPage /> }],
  },
];

describe("overview page", () => {
  it("shows web numbers from the web totals, api numbers and one line per other page", async () => {
    const urls = stub({ pageviews: 120, ingest: { accepted: 5 }, api: "used" });
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}`);
    await waitFor(() =>
      expect(host.querySelector('[data-metric="pageviews"]')?.textContent).toContain("120"),
    );
    // +50% against the previous 2 days of the web totals, never the edge counts
    expect(host.querySelector('[data-metric="pageviews"]')?.textContent).toContain("+50%");
    await waitFor(() =>
      expect(host.querySelector('[data-tile="API requests"]')?.textContent).toContain("1,200"),
    );
    await waitFor(() =>
      expect(host.querySelector('[data-summary="On-chain"]')?.textContent).toContain(
        "1,234operations of verified ids, 1 of 2 verified",
      ),
    );
    expect(host.querySelector('[data-summary="Integrity grade"] [data-grade]')).not.toBeNull();
    const links = [...host.querySelectorAll("a")].map((a) => a.getAttribute("href"));
    expect(links).toContain(`/projects/${f.TENANT_ID}/scf`);
    expect(links).toContain(`/projects/${f.TENANT_ID}/onchain`);
    expect(links).toContain(`/projects/${f.TENANT_ID}/sites`);
    // the api asks for the same utc days as the web totals
    const api = decodeURIComponent(urls.find((u) => u.includes("/api-metrics/overview")) ?? "");
    expect(api).toContain("range=custom");
    expect(api).toMatch(/from=\d{4}-\d{2}-\d{2}T00:00:00Z/);
    // no full integrity or on-chain panel
    expect(host.querySelector('[aria-label="Integrity"]')).toBeNull();
    expect(host.textContent).not.toContain("Weekly active accounts");
    expect(host.querySelector("[data-freshness]")?.textContent).toContain("Updated");
  });

  it("names the next setup step, and completes the first event on the web totals", async () => {
    stub({ pageviews: 0, ingest: {}, api: "none" });
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}`);
    await waitFor(() => expect(host.querySelector('[aria-label="Next step"]')).not.toBeNull());
    const step = host.querySelector('[aria-label="Next step"]');
    expect(step?.getAttribute("data-step")).toBe("install");
    expect(step?.textContent).toContain("Continue setup");
    expect(host.querySelector('[data-api="none"]')?.textContent).toContain("Connect API");

    stub({ pageviews: 7, ingest: {}, api: "none" });
    const again = await renderRoutes(routes, `/projects/${f.TENANT_ID}`);
    // the edge has no event, the web totals have one: the next step is the domain
    await waitFor(() =>
      expect(again.querySelector('[aria-label="Next step"]')?.getAttribute("data-step")).toBe(
        "verify",
      ),
    );
  });

  const stopped = (status: ProjectContext["tenant"]["status"], isOwner = true) => [
    {
      path: "/projects/:tenantId",
      element: (
        <TooltipProvider>
          <Outlet
            context={{ ...ctx, isOwner, tenant: { ...ctx.tenant, status } } as ProjectContext}
          />
        </TooltipProvider>
      ),
      children: [{ index: true, element: <OverviewPage /> }],
    },
  ];

  it("offers no setup step on a project that is not approved, suspended or removed", async () => {
    for (const status of ["rejected", "suspended", "removed"] as const) {
      const urls = stub({ pageviews: 0, ingest: {}, api: "none" });
      const host = await renderRoutes(stopped(status), `/projects/${f.TENANT_ID}`);
      await waitFor(() =>
        expect(host.querySelector('[data-metric="pageviews"]')?.textContent).toContain("0"),
      );
      // the same site is at its install step on an active project (above)
      expect(host.querySelector('[aria-label="Next step"]')).toBeNull();
      expect(host.textContent).not.toContain("Continue setup");
      // the setup step is not even read
      expect(urls.some((u) => u.includes("/ingest"))).toBe(false);
    }
  });

  it("offers Add site where the control plane takes a site, and says what to do", async () => {
    const noSites = () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input: string) => {
          const u = new URL(input, "https://app.example.org");
          const body = u.pathname.endsWith("/sites")
            ? { ...f.sitePageBody, items: [] }
            : u.pathname.endsWith("/integrity")
              ? integrityReportBody
              : u.pathname.endsWith("/onchain")
                ? onchainBody
                : null;
          return new Response(JSON.stringify(body ?? { error: { code: "unavailable" } }), {
            status: body ? 200 : 503,
            headers: { "Content-Type": "application/json; charset=utf-8" },
          });
        }),
      );
    };
    const buttons = (host: HTMLElement) =>
      [...host.querySelectorAll("button")].map((b) => b.textContent?.trim());
    // an active project starts with the setup checklist and its Add site
    noSites();
    const active = await renderRoutes(stopped("active"), `/projects/${f.TENANT_ID}`);
    await waitFor(() => expect(active.textContent).toContain("Set up the project"));
    expect(active.querySelector('[data-check="1"]')?.getAttribute("data-state")).toBe("current");
    expect(buttons(active)).toContain("Add site");

    // the control plane takes sites on a project that is not approved: they
    // wait for review. a suspended project refuses them.
    const cases = [
      ["rejected", true, "Change the answers above and submit them again.", true],
      ["rejected", false, "An owner can change the answers", true],
      ["suspended", true, "You cannot add sites while the project is suspended.", false],
      ["removed", true, "This project was removed. It cannot take new sites.", false],
    ] as const;
    for (const [status, isOwner, text, add] of cases) {
      noSites();
      const host = await renderRoutes(stopped(status, isOwner), `/projects/${f.TENANT_ID}`);
      await waitFor(() => expect(host.textContent).toContain("No sites yet"));
      expect(host.textContent).toContain(text);
      expect(host.textContent).not.toContain("Set up the project");
      if (add) expect(buttons(host)).toContain("Add site");
      else expect(buttons(host)).not.toContain("Add site");
    }
  });
});

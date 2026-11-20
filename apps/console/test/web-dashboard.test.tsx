import { Outlet } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { webBreakdown, webRealtime, webSessions, webSummary } from "@/api/web";
import { TooltipProvider } from "@/components/ui/tooltip";
import { countryName } from "@/lib/format";
import {
  formatMetric,
  lastDaysOf,
  readWebState,
  validateWebDays,
  webDays,
  webPath,
  webQuery,
  webTrend,
  writeWebState,
} from "@/lib/web-dashboard";
import { SessionFinder } from "@/pages/project/privacy";
import { WebPage } from "@/pages/project/web";
import type { ProjectContext } from "@/pages/project-layout";
import * as f from "./fixtures/contract";
import { byRole, click, render, renderRoutes, stubFetch, waitFor } from "./helpers";

const NOW = new Date("2026-11-16T15:04:05Z");

describe("web dashboard state", () => {
  it("reads and writes the address", () => {
    const s = readWebState(new URLSearchParams(""));
    expect(s).toEqual({
      preset: "30",
      custom: { from: "", to: "" },
      site: null,
      band: "primary",
      metric: "pageviews",
    });
    expect(writeWebState(s).toString()).toBe("");
    const custom = readWebState(
      new URLSearchParams(
        `range=custom&from=2026-11-01&to=2026-11-03&site=${f.SITE_ID}&band=quarantine&metric=bounce_rate`,
      ),
    );
    expect(custom.preset).toBe("custom");
    expect(webDays(custom, NOW)).toEqual({ from: "2026-11-01", to: "2026-11-03" });
    expect(webQuery(custom, NOW)).toEqual({
      from: "2026-11-01",
      to: "2026-11-03",
      site: f.SITE_ID,
      band: "quarantine",
    });
    expect(readWebState(writeWebState(custom))).toEqual(custom);
    // a malformed value falls back to the default, never to a wider query
    const bad = readWebState(
      new URLSearchParams("range=custom&from=x&site=../x&band=all&metric=title"),
    );
    expect(bad.preset).toBe("30");
    expect(bad.site).toBeNull();
    expect(bad.band).toBe("primary");
    expect(bad.metric).toBe("pageviews");
  });

  it("resolves the presets to utc days", () => {
    const at = (preset: string) =>
      webDays(readWebState(new URLSearchParams(`range=${preset}`)), NOW);
    expect(at("today")).toEqual({ from: "2026-11-16", to: "2026-11-16" });
    expect(at("7")).toEqual({ from: "2026-11-10", to: "2026-11-16" });
    expect(at("90")).toEqual({ from: "2026-08-19", to: "2026-11-16" });
  });

  it("checks a custom range before asking", () => {
    expect(validateWebDays({ from: "2026-11-01", to: "2026-11-17" }, NOW).to).toContain("today");
    expect(validateWebDays({ from: "2026-11-05", to: "2026-11-01" }, NOW).to).toContain("start");
    expect(validateWebDays({ from: "2025-10-12", to: "2026-11-16" }, NOW).to).toBe(
      "A range covers at most 400 days.",
    );
    expect(validateWebDays({ from: "2025-11-16", to: "2026-11-16" }, NOW)).toEqual({});
    // 367 to 400 days are allowed (18.1), more than the 366 of the operator views
    expect(validateWebDays({ from: "2025-11-15", to: "2026-11-16" }, NOW)).toEqual({});
    expect(validateWebDays({ from: "2025-10-13", to: "2026-11-16" }, NOW)).toEqual({});
  });

  it("cuts a range to the days a neighbouring view allows", () => {
    expect(lastDaysOf({ from: "2026-11-01", to: "2026-11-16" }, 366)).toEqual({
      from: "2026-11-01",
      to: "2026-11-16",
    });
    expect(lastDaysOf({ from: "2025-10-13", to: "2026-11-16" }, 366)).toEqual({
      from: "2025-11-16",
      to: "2026-11-16",
    });
  });

  it("compares with the previous period", () => {
    expect(webTrend("pageviews", 150, 100, 7)).toEqual({
      text: "+50%",
      direction: "up",
      good: true,
      period: "previous 7 days",
    });
    // a higher bounce rate is worse, and it moves in points
    expect(webTrend("bounce_rate", 0.5, 0.4, 1)).toEqual({
      text: "+10 pp",
      direction: "up",
      good: false,
      period: "previous day",
    });
    expect(webTrend("visitors", 10, 0, 7)).toBeNull();
    expect(webTrend("avg_visit_duration", null, 30, 7)).toBeNull();
    expect(webTrend("visits", 100, 100, 7)?.text).toBe("no change");
  });

  it("formats values", () => {
    expect(formatMetric("pageviews", 12345)).toBe("12,345");
    expect(formatMetric("bounce_rate", 0.4567)).toBe("46%");
    expect(formatMetric("bounce_rate", 0.0456)).toBe("4.6%");
    expect(formatMetric("bounce_rate", null)).toBe("–");
    expect(countryName("DE")).toBe("Germany");
    expect(countryName("XX")).toBe("XX");
    expect(webPath(f.TENANT_ID, f.SITE_ID)).toBe(`/projects/${f.TENANT_ID}/web?site=${f.SITE_ID}`);
  });
});

const days = ["2026-11-14", "2026-11-15", "2026-11-16"];
const point = (day: string, pv: number | null) => ({
  day,
  aggregated: pv !== null,
  pageviews: pv,
  visitors: pv === null ? null : Math.ceil(pv / 2),
  visits: pv === null ? null : Math.ceil(pv / 2),
  bounces: pv === null ? null : 1,
  totaltime: pv === null ? null : 60,
  bounce_rate: pv === null ? null : 0.5,
  avg_visit_duration: pv === null ? null : 30,
});
const siteRef = { id: f.SITE_ID, name: "Marketing site", domain: "example.org", status: "active" };

const summaryBody = {
  band: "primary",
  site: null,
  sites: [siteRef],
  current: {
    range: { from: days[0], to: days[2], days: 3 },
    totals: {
      pageviews: 1200,
      visitors: 400,
      visits: 500,
      bounces: 200,
      totaltime: 30000,
      bounce_rate: 0.4,
      avg_visit_duration: 60,
      aggregated_days: 3,
    },
    series: days.map((d, i) => point(d, 400 + i)),
  },
  previous: {
    range: { from: "2026-11-11", to: "2026-11-13", days: 3 },
    totals: {
      pageviews: 1000,
      visitors: 400,
      visits: 400,
      bounces: 200,
      totaltime: 24000,
      bounce_rate: 0.5,
      avg_visit_duration: 60,
      aggregated_days: 3,
    },
    series: ["2026-11-11", "2026-11-12", "2026-11-13"].map((d) => point(d, 333)),
  },
  other_band: { band: "quarantine", pageviews: 300, visitors: 20 },
  quarantine_share: 0.2,
  read_at: "2026-11-16T15:00:00Z",
  source: "aggregate",
};

const breakdownBody = (dimension: string, items: { key: string; value: number }[]) => ({
  dimension,
  band: "primary",
  range: { from: days[0], to: days[2], days: 3 },
  source: ["referrer", "country", "browser", "os", "device"].includes(dimension)
    ? "aggregate"
    : "engine",
  unit: dimension === "event" ? "events" : "visitors",
  items,
  total_keys: items.length + (dimension === "page" ? 20 : 0),
  sites: null,
});

const realtimeBody = {
  at: "2026-11-16T15:04:05Z",
  source: "engine",
  visitors: 7,
  sites: [{ site: siteRef, visitors: 7 }],
  minutes: Array.from({ length: 30 }, (_, i) => ({
    at: new Date(Date.UTC(2026, 10, 16, 14, 35 + i)).toISOString(),
    pageviews: i % 3,
    visitors: i % 2,
  })),
};

const ctx: ProjectContext = {
  tenant: f.tenantBody as ProjectContext["tenant"],
  me: f.meBody as ProjectContext["me"],
  isOwner: true,
  termsCurrent: true,
};

// a fetch double that answers by route, since the dashboard asks for its
// panels at the same time and their order is not fixed
function stubRoutes(engine: "ok" | "pending" = "ok") {
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      urls.push(input);
      const u = new URL(input, "https://app.example.org");
      const json = (status: number, body: unknown) =>
        new Response(JSON.stringify(body), {
          status,
          headers: { "Content-Type": "application/json; charset=utf-8" },
        });
      if (u.pathname.endsWith("/sites")) return json(200, f.sitePageBody);
      if (u.pathname.endsWith("/web/summary")) return json(200, summaryBody);
      if (u.pathname.endsWith("/ingest")) return json(200, { ...f.ingestBody, bucket: "day" });
      if (engine === "pending" && u.pathname.endsWith("/web/realtime"))
        return json(409, f.errorBody("provisioning_pending"));
      if (u.pathname.endsWith("/web/realtime")) return json(200, realtimeBody);
      if (u.pathname.endsWith("/web/breakdown")) {
        const d = u.searchParams.get("dimension") ?? "";
        const items: Record<string, { key: string; value: number }[]> = {
          page: [
            { key: "/", value: 300 },
            { key: "/pricing", value: 120 },
          ],
          country: [
            { key: "DE", value: 200 },
            { key: "BR", value: 50 },
          ],
          referrer: [{ key: "github.com", value: 90 }],
          browser: [{ key: "chrome", value: 300 }],
          event: [{ key: "signup", value: 42 }],
        };
        return json(200, breakdownBody(d, items[d] ?? []));
      }
      return json(503, { error: { code: "unavailable", message: "not in this test" } });
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
    children: [{ path: "web", element: <WebPage /> }],
  },
];

describe("web dashboard page (contract 18)", () => {
  it("parses the contract's answers", () => {
    expect(webSummary.parse(summaryBody).current.totals.pageviews).toBe(1200);
    expect(webBreakdown.parse(breakdownBody("page", [{ key: "/", value: 1 }])).unit).toBe(
      "visitors",
    );
    expect(webRealtime.parse(realtimeBody).minutes).toHaveLength(30);
    expect(() => webSummary.parse({ ...summaryBody, band: "all" })).toThrow();
  });

  it("shows every metric with its trend, the breakdowns and the visitors right now", async () => {
    const urls = stubRoutes();
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}/web`);
    await waitFor(() => expect(host.textContent).toContain("Unique visitors"));
    await waitFor(() => expect(host.textContent).toContain("1,200"));
    const totals = host.querySelector('[aria-label="Totals"]')?.textContent ?? "";
    for (const label of [
      "Page views",
      "Unique visitors",
      "Visits",
      "Bounce rate",
      "Visit duration",
    ])
      expect(totals).toContain(label);
    expect(totals).toContain("20%");
    expect(totals).toContain("40%");
    expect(totals).toContain("10 pp");
    // each breakdown loads on its own, so each is awaited
    await waitFor(() => expect(host.textContent).toContain("Germany"));
    await waitFor(() => expect(host.textContent).toContain("/pricing"));
    await waitFor(() => expect(host.textContent).toContain("github.com"));
    await waitFor(() => expect(host.textContent).toContain("signup"));
    // shares are of the range's visitors
    expect(host.textContent).toContain("75%");
    await waitFor(() =>
      expect(host.querySelector('[data-testid="web-active"]')?.textContent).toBe("7"),
    );
    // where every event went, with the reasons
    await waitFor(() => expect(host.textContent).toContain("Origin not allowed"));
    const went = host.querySelector('[aria-label="Where events went"]')?.textContent ?? "";
    expect(went).toContain("Counted");
    expect(went).toContain("Rejected");
    expect(went).toContain("17");
    expect(host.querySelector("[data-freshness]")?.textContent).toContain("Updated");
    // the default range is the last 30 utc days, the whole project, counted traffic
    const summary = urls.find((u) => u.includes("/web/summary")) ?? "";
    expect(summary).toContain(`/tenants/${f.TENANT_ID}/web/summary?from=`);
    expect(summary).not.toContain("site=");
    expect(summary).not.toContain("band=");
    // utm is offered only for a site that collects it
    expect(host.textContent).not.toContain("Campaign");
    // more values of a breakdown are one press away
    await click(byRole(host, "button", "Show all 22"));
    await waitFor(() =>
      expect(urls.some((u) => u.includes("dimension=page") && u.includes("limit=50"))).toBe(true),
    );
  });

  it("asks for one site's quarantined traffic and draws a day per hour", async () => {
    const urls = stubRoutes();
    const host = await renderRoutes(
      routes,
      `/projects/${f.TENANT_ID}/web?site=${f.SITE_ID}&band=quarantine&range=today`,
    );
    await waitFor(() => expect(host.textContent).toContain("Unique visitors"));
    const summary = urls.find((u) => u.includes("/web/summary")) ?? "";
    expect(summary).toContain(`site=${f.SITE_ID}`);
    expect(summary).toContain("band=quarantine");
    expect(urls.some((u) => u.includes("/web/hourly"))).toBe(true);
    // quarantined traffic has no realtime strip
    expect(urls.some((u) => u.includes("/web/realtime"))).toBe(false);
  });
});

describe("web dashboard address", () => {
  it("shows all sites, and says so, for a site that is not the project's", async () => {
    const urls = stubRoutes();
    const other = "0b8f7c1e-1111-4222-8333-444455556666";
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}/web?site=${other}`);
    await waitFor(() => expect(host.textContent).toContain("Unique visitors"));
    expect(host.textContent).toContain("That site is not part of this project");
    // no request names the unknown site; the figures are those of all sites
    expect(urls.some((u) => u.includes(other))).toBe(false);
    const summary = urls.find((u) => u.includes("/web/summary")) ?? "";
    expect(summary).not.toContain("site=");
    await click(byRole(host, "button", "Show all sites"));
    await waitFor(() =>
      expect(host.textContent).not.toContain("That site is not part of this project"),
    );
  });

  it("says why live figures wait while analytics access is being set up", async () => {
    stubRoutes("pending");
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}/web`);
    await waitFor(() => expect(host.textContent).toContain("Live figures are being set up"));
    expect(host.textContent).not.toContain("unavailable");
  });

  it("asks for the outcome counts of the chosen days and no integrity or on-chain panel", async () => {
    const urls = stubRoutes();
    const host = await renderRoutes(
      routes,
      `/projects/${f.TENANT_ID}/web?range=custom&from=2026-10-01&to=2026-10-31`,
    );
    // the site chips read the last 30 days; where events went reads the range
    const ranged = (u: string) => decodeURIComponent(u).includes("from=2026-10-01");
    await waitFor(() => expect(urls.some((u) => u.includes("/ingest?") && ranged(u))).toBe(true));
    const ingest = decodeURIComponent(urls.find((u) => u.includes("/ingest?") && ranged(u)) ?? "");
    expect(ingest).toContain(`/sites/${f.SITE_ID}/ingest?from=2026-10-01T00:00:00Z`);
    expect(ingest).toContain("to=2026-11-01T00:00:00Z&bucket=day");
    await waitFor(() => expect(host.textContent).toContain("Unique visitors"));
    expect(urls.some((u) => u.includes("/onchain") || u.includes("/integrity"))).toBe(false);
  });

  it("switches to quarantined traffic from the header", async () => {
    const urls = stubRoutes();
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}/web`);
    await waitFor(() => expect(host.textContent).toContain("Unique visitors"));
    await click(byRole(host, "radio", "Quarantined"));
    await waitFor(() =>
      expect(host.querySelector('[data-testid="web-dashboard"]')?.getAttribute("data-band")).toBe(
        "quarantine",
      ),
    );
    expect(urls.some((u) => u.includes("band=quarantine"))).toBe(true);
    expect(host.querySelector('[data-testid="web-active"]')).toBeNull();
  });
});

describe("session finder of the privacy page (contract 10.2 and 18)", () => {
  it("lists a day's sessions and hands the chosen id to the form", async () => {
    const body = {
      day: "2026-11-16",
      band: "primary",
      site: siteRef,
      source: "engine",
      page: 1,
      page_size: 50,
      total: 2,
      items: [
        {
          id: "2f1c7a9e-5b3d-4c8e-9a1f-6d0b2e4c8a7f",
          first_at: "2026-11-16T10:00:00Z",
          last_at: "2026-11-16T10:05:00Z",
          visits: 1,
          views: 3,
          country: "DE",
          browser: "chrome",
          os: "Linux",
          device: "laptop",
        },
        {
          id: "3a2b7a9e-5b3d-4c8e-9a1f-6d0b2e4c8a70",
          first_at: "2026-11-16T09:00:00Z",
          last_at: "2026-11-16T09:00:00Z",
          visits: 1,
          views: 1,
          country: "",
          browser: "safari",
          os: "iOS",
          device: "mobile",
        },
      ],
    };
    expect(webSessions.parse(body).items).toHaveLength(2);
    const calls = stubFetch([{ status: 200, body }]);
    let chosen = "";
    const site = { ...f.siteBody } as Parameters<typeof SessionFinder>[0]["site"];
    const host = await render(
      <SessionFinder
        tenantId={f.TENANT_ID}
        site={site}
        chosen=""
        onChoose={(id) => {
          chosen = id;
        }}
      />,
    );
    await waitFor(() => expect(host.textContent).toContain("3 page views"));
    expect(host.textContent).toContain("1 page view");
    expect(calls[0]?.url).toContain(`/tenants/${f.TENANT_ID}/web/sessions?site=${f.SITE_ID}&day=`);
    // the engine's keys read as names
    expect(host.textContent).toContain("Chrome, Linux, Laptop");
    expect(host.textContent).toContain("Germany");
    expect(host.textContent).toContain("Safari, iOS, Mobile");
    const first = [...host.querySelectorAll("button")].find((b) =>
      (b.textContent ?? "").includes("Chrome"),
    );
    if (!first) throw new Error("no session button");
    await click(first);
    expect(chosen).toBe("2f1c7a9e-5b3d-4c8e-9a1f-6d0b2e4c8a7f");
  });

  it("lists quarantined sessions when asked", async () => {
    const body = {
      day: "2026-11-16",
      band: "quarantine",
      site: siteRef,
      source: "engine",
      page: 1,
      page_size: 50,
      total: 0,
      items: [],
    };
    const calls = stubFetch([
      { status: 200, body: { ...body, band: "primary" } },
      { status: 200, body },
    ]);
    const site = { ...f.siteBody } as Parameters<typeof SessionFinder>[0]["site"];
    const host = await render(
      <SessionFinder tenantId={f.TENANT_ID} site={site} chosen="" onChoose={() => {}} />,
    );
    await waitFor(() => expect(host.textContent).toContain("No sessions on this day."));
    expect(calls[0]?.url).not.toContain("band=");
    await click(byRole(host, "radio", "Quarantined"));
    await waitFor(() => expect(calls.length).toBe(2));
    expect(calls[1]?.url).toContain("band=quarantine");
  });
});

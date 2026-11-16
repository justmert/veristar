import { Outlet } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { webBreakdown, webRealtime, webSummary } from "@/api/web";
import { TooltipProvider } from "@/components/ui/tooltip";
import {
  countryName,
  formatMetric,
  readWebState,
  validateWebDays,
  webDays,
  webPath,
  webQuery,
  webTrend,
  writeWebState,
} from "@/lib/web-dashboard";
import type { ProjectContext } from "@/pages/project-layout";
import { WebPage } from "@/pages/web";
import * as f from "./fixtures/contract";
import { byRole, click, renderRoutes, waitFor } from "./helpers";

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
    expect(validateWebDays({ from: "2025-10-12", to: "2026-11-16" }, NOW).to).toBeDefined();
    expect(validateWebDays({ from: "2025-11-16", to: "2026-11-16" }, NOW)).toEqual({});
  });

  it("compares with the previous period", () => {
    expect(webTrend("pageviews", 150, 100, 7)).toEqual({
      text: "50.0%",
      direction: "up",
      good: true,
      period: "previous 7 days",
    });
    // a higher bounce rate is worse, and it moves in points
    expect(webTrend("bounce_rate", 0.5, 0.4, 1)).toEqual({
      text: "10.0 pt",
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
    expect(formatMetric("bounce_rate", 0.4567)).toBe("45.7%");
    expect(formatMetric("bounce_rate", null)).toBe("n/a");
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
function stubRoutes() {
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
    expect(totals).toContain("20.0%");
    expect(totals).toContain("40.0%");
    expect(totals).toContain("10.0 pt");
    await waitFor(() => expect(host.textContent).toContain("Germany"));
    expect(host.textContent).toContain("/pricing");
    expect(host.textContent).toContain("github.com");
    expect(host.textContent).toContain("signup");
    // shares are of the range's visitors
    expect(host.textContent).toContain("75.0%");
    await waitFor(() =>
      expect(host.querySelector('[data-testid="web-active"]')?.textContent).toBe("7"),
    );
    expect(host.textContent).toContain("20.0% of page views were quarantined");
    expect(host.textContent).toContain("Totals updated");
    // the default range is the last 30 utc days, the whole project, counted traffic
    const summary = urls.find((u) => u.includes("/web/summary")) ?? "";
    expect(summary).toContain(`/tenants/${f.TENANT_ID}/web/summary?from=`);
    expect(summary).not.toContain("site=");
    expect(summary).not.toContain("band=");
    // utm is offered only for a site that collects it
    expect(host.textContent).not.toContain("Campaign");
    // more values of a breakdown are one press away
    await click(byRole(host, "button", "Show up to 100"));
    await waitFor(() =>
      expect(urls.some((u) => u.includes("dimension=page") && u.includes("limit=100"))).toBe(true),
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

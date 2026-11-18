import { act } from "react";
import { Outlet } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { apiMetricsPath } from "@/api/endpoints";
import {
  apiOverview,
  apiRoutes,
  apiServices,
  apiStatus,
  apiTimeseries,
  apiWarnings,
  type Tenant,
} from "@/api/schemas";
import { TooltipProvider } from "@/components/ui/tooltip";
import {
  instantToField,
  isPartialBucket,
  latencyText,
  percentileChange,
  rateChange,
  rateText,
  readState,
  relativeChange,
  toStatChange,
  validateCustomEnd,
  writeState,
} from "@/lib/api-metrics";
import { ApiPage, plotLatency, rateTone, sparkValues, standouts } from "@/pages/project/api";
import * as f from "./fixtures/contract";
import { byLabel, click, renderRoutes, type, waitFor } from "./helpers";

// jsdom has no IntersectionObserver; the charts draw in once they are in
// view. every supported browser has it, so this only fills the test dom.
if (!("IntersectionObserver" in globalThis)) {
  globalThis.IntersectionObserver = class {
    readonly root = null;
    readonly rootMargin = "";
    readonly thresholds = [];
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return [];
    }
  } as unknown as typeof IntersectionObserver;
}

describe("api metrics schemas accept the contract's shapes (section 7)", () => {
  it.each([
    ["overview (7.5)", apiOverview, f.apiOverviewBody],
    ["timeseries (7.6)", apiTimeseries, f.apiTimeseriesBody],
    ["routes (7.7)", apiRoutes, f.apiRoutesBody],
    ["status (7.8)", apiStatus, f.apiStatusBody],
    ["services (7.9)", apiServices, f.apiServicesBody],
    ["services, empty (7.9)", apiServices, f.apiServicesEmptyBody],
    ["warnings (7.10)", apiWarnings, f.apiWarningsBody],
    [
      "overview after the retention step (7.2)",
      apiOverview,
      {
        ...f.apiOverviewBody,
        previous: {
          ...f.apiOverviewBody.previous,
          range: {
            ...f.apiPreviousRangeBody,
            resolution: "day",
            bucket_seconds: 86400,
            resolution_note: "older_than_hourly_retention",
          },
          summary: f.apiEmptySummaryBody,
          sources: [],
        },
      },
    ],
  ])("%s", (_name, schema, body) => {
    const r = schema.safeParse(body);
    expect(r.success, JSON.stringify(r.error?.issues)).toBe(true);
  });

  it.each([
    [
      "a percentile in the open bucket written as a number string",
      apiOverview,
      {
        ...f.apiOverviewBody,
        current: { ...f.apiOverviewBody.current, summary: { ...f.apiSummaryBody, p99: "10" } },
      },
    ],
    [
      "a rate above 1",
      apiRoutes,
      { ...f.apiRoutesBody, items: [{ ...f.apiRouteItemBody, rate_4xx: 1.2 }] },
    ],
    [
      "an unknown source",
      apiServices,
      { ...f.apiServicesBody, items: [{ ...f.apiServicesBody.items[0], source: "log" }] },
    ],
    [
      "status classes out of order",
      apiStatus,
      { ...f.apiStatusBody, classes: [...f.apiStatusBody.classes].reverse() },
    ],
    [
      "a missing warning kind",
      apiWarnings,
      { ...f.apiWarningsBody, totals: f.apiWarningsBody.totals.slice(1) },
    ],
    [
      "an unknown resolution",
      apiServices,
      { ...f.apiServicesBody, range: { ...f.apiRangeBody, resolution: "week" } },
    ],
    ["sources as null", apiTimeseries, { ...f.apiTimeseriesBody, sources: null }],
  ])("refuses %s", (_name, schema, body) => {
    expect(schema.safeParse(body).success).toBe(false);
  });
});

describe("api metrics formats and url state", () => {
  it("never shows the open-ended top bucket as a number", () => {
    expect(latencyText(">10 s")).toBe(">10 s");
    expect(latencyText(null)).toBeNull();
    expect(latencyText(0.042)).toBe("42 ms");
    expect(latencyText(0.0042)).toBe("4 ms");
    expect(latencyText(2.5)).toBe("2.5 s");
    expect(latencyText(10)).toBe("10 s");
  });

  it("formats rates as percentages and changes of rates in points", () => {
    expect(rateText(0.025)).toBe("2.5%");
    expect(rateText(0.0016666666666666668)).toBe("0.2%");
    expect(rateText(0)).toBe("0%");
    expect(rateText(0.000001)).toBe("<0.1%");
    expect(rateText(null)).toBeNull();
    expect(rateChange(0.025, 0.02)).toEqual({ text: "+0.5 pp", direction: "up" });
    expect(rateChange(0.01, null).direction).toBeNull();
    expect(relativeChange(1200, 1000)).toEqual({ text: "+20%", direction: "up" });
    expect(relativeChange(5, 0).direction).toBeNull();
    expect(percentileChange(">10 s", 0.9)).toEqual({ text: "was 900 ms", direction: "up" });
    expect(percentileChange(0.042, 0.05)).toEqual({ text: "-16%", direction: "down" });
  });

  it("keeps an empty filter apart from an absent one (contract 7.1)", () => {
    const s = readState(new URLSearchParams("range=7d&service=&method=GET"));
    expect(s.filters).toEqual({ service: "", route: null, method: "GET" });
    expect(writeState(s).toString()).toBe("range=7d&service=&method=GET");
    expect(apiMetricsPath(f.TENANT_ID, "overview", s.range, s.filters)).toBe(
      `/tenants/${f.TENANT_ID}/api-metrics/overview?range=7d&service=&method=GET`,
    );
  });

  it("encodes filter values and leaves filters off services and warnings", () => {
    const filters = { service: "a&b", route: "/v1/x/{id}", method: null };
    expect(
      apiMetricsPath(f.TENANT_ID, "routes", { range: "1h" }, filters, {
        sort: "p95",
        order: "asc",
        limit: 50,
        cursor: "c1",
      }),
    ).toBe(
      `/tenants/${f.TENANT_ID}/api-metrics/routes?range=1h&service=a%26b&route=%2Fv1%2Fx%2F%7Bid%7D&sort=p95&order=asc&limit=50&cursor=c1`,
    );
    expect(apiMetricsPath(f.TENANT_ID, "services", { range: "1h" }, null)).toBe(
      `/tenants/${f.TENANT_ID}/api-metrics/services?range=1h`,
    );
  });

  it("reads a custom range only with both ends and falls back to the default preset", () => {
    expect(
      readState(
        new URLSearchParams("range=custom&from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z"),
      ).range,
    ).toEqual({ range: "custom", from: "2026-11-01T00:00:00Z", to: "2026-11-02T00:00:00Z" });
    expect(readState(new URLSearchParams("range=custom&from=2026-11-01T00:00:00Z")).range).toEqual({
      range: "24h",
    });
    expect(readState(new URLSearchParams("range=5y")).range).toEqual({ range: "24h" });
    const s = readState(new URLSearchParams("sort=route"));
    expect([s.sort, s.order]).toEqual(["route", "asc"]);
  });

  it("checks the end of a custom range and shows an instant as the field takes it", () => {
    const now = new Date("2026-11-12T10:00:00Z");
    expect(validateCustomEnd("2026-11-13T09:00:00Z", now)).toBeNull();
    expect(validateCustomEnd("2026-11-14T00:00:00Z", now)).toBe(
      "End no later than 24 hours from now.",
    );
    expect(instantToField("2026-11-01T08:30:00Z")).toBe("2026-11-01 08:30");
  });

  it("marks the running bucket as partial", () => {
    const range = apiServices.parse(f.apiServicesBody).range;
    const at = range.from;
    const step = range.bucket_seconds * 1000;
    expect(isPartialBucket(at, range, Date.parse(at) + step - 1)).toBe(true);
    expect(isPartialBucket(at, range, Date.parse(at) + step)).toBe(false);
  });

  it("turns a change into a stat card line, and says nothing without a previous value", () => {
    expect(toStatChange(relativeChange(1200, 1000), null)).toEqual({
      text: "+20%",
      direction: "up",
      good: null,
      period: "previous range",
    });
    expect(toStatChange(rateChange(0.03, 0.02), true)?.good).toBe(false);
    expect(toStatChange(rateChange(0.02, 0.02), true)).toMatchObject({
      direction: "flat",
      good: null,
    });
    expect(toStatChange(relativeChange(5, 0), null)).toBeNull();
  });

  it("colours rates by their marks, plots the open bucket on the 10 s line and skips flat sparklines", () => {
    expect(rateTone("4xx", 0.049)).toBeUndefined();
    expect(rateTone("4xx", 0.05)).toBe("text-warning");
    expect(rateTone("5xx", 0.0001)).toBe("text-critical");
    expect(rateTone("5xx", 0)).toBeUndefined();
    const open = plotLatency(">10 s") ?? 0;
    expect(open).toBeGreaterThan(10);
    expect(open).toBeLessThan(10.01);
    expect(plotLatency(null)).toBeNull();
    const points = apiTimeseries.parse(f.apiTimeseriesBody).points;
    expect(sparkValues(points.slice(0, 1), (p) => p.requests)).toBeNull();
    expect(sparkValues(points, () => 3)).toBeNull();
    const items = apiRoutes.parse(f.apiRoutesBody).items;
    expect(standouts(items).slow?.route).toBe("/jobs/{id}");
  });
});

// a fetch double that answers by api path, so the test does not depend on
// the order in which the page's queries start; it only stands in for the
// network in tests
function serve(bodies: Partial<Record<string, unknown>>) {
  const calls: URL[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const u = new URL(input, "https://app.test");
      calls.push(u);
      const kind = u.pathname.split("/").pop() ?? "";
      const body = bodies[kind];
      if (body === undefined) throw new Error(`unexpected request ${input}`);
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }),
  );
  return {
    calls,
    last: (kind: string) => {
      const c = calls.filter((u) => u.pathname.endsWith(`/api-metrics/${kind}`));
      return c[c.length - 1];
    },
  };
}

const full = {
  services: f.apiServicesBody,
  warnings: f.apiWarningsBody,
  overview: f.apiOverviewBody,
  timeseries: f.apiTimeseriesBody,
  status: f.apiStatusBody,
  routes: f.apiRoutesBody,
};

const path = `/projects/${f.TENANT_ID}/api`;

function routes() {
  const ctx = { tenant: f.tenantBody as Tenant, me: f.meBody, isOwner: true, termsCurrent: true };
  return [
    {
      path: "/projects/:tenantId",
      // the shell provides the tooltip layer in the app
      element: (
        <TooltipProvider>
          <Outlet context={ctx} />
        </TooltipProvider>
      ),
      children: [
        { path: "api", element: <ApiPage /> },
        { path: "connect-api", element: <p>setup page</p> },
      ],
    },
  ];
}

function text(el: Element | null | undefined): string {
  return (el?.textContent ?? "").replace(/\s+/g, " ").trim();
}

function cells(el: Element | null | undefined): string[] {
  return [...(el?.children ?? [])].map((c) => text(c));
}

function tile(host: HTMLElement, label: string): string {
  return text(host.querySelector(`[data-tile="${label}"]`));
}

// the range is a radio group of buttons, named by their short labels
function radio(host: HTMLElement, label: string): HTMLButtonElement {
  const group = host.querySelector('[role="radiogroup"][aria-label="Range"]');
  const el = [...(group?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [])].find(
    (b) => text(b) === label,
  );
  if (!el) throw new Error(`no range ${label}`);
  return el;
}

function header(host: HTMLElement, label: string): HTMLButtonElement {
  const b = [...host.querySelectorAll<HTMLButtonElement>("th button")].find(
    (x) => text(x) === label,
  );
  if (!b)
    throw new Error(
      `no column ${label} in ${[...host.querySelectorAll("th button")].map((x) => text(x)).join("|")}`,
    );
  return b;
}

describe("api traffic page", () => {
  it("shows the empty state with a link to the setup page", async () => {
    const net = serve({ services: f.apiServicesEmptyBody, warnings: f.apiWarningsNoneBody });
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No API requests in this range"));
    const link = [...host.querySelectorAll("a")].find((a) => text(a) === "Connect API");
    expect(link?.getAttribute("href")).toBe(`/projects/${f.TENANT_ID}/connect-api`);
    // no summary is asked for when no service sent anything
    expect(net.calls.map((u) => u.pathname.split("/").pop()).sort()).toEqual([
      "services",
      "warnings",
    ]);
    expect(net.last("services")?.search).toBe("?range=24h");
  });

  it("shows the summary against the previous range, with the open bucket as >10 s", async () => {
    serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(tile(host, "p99")).toContain(">10 s"));
    expect(tile(host, "Requests")).toContain("1,200");
    expect(tile(host, "Requests")).toContain("+20%");
    expect(tile(host, "4xx rate")).toContain("2.5%");
    expect(tile(host, "4xx rate")).toContain("30 client errors");
    // the sign says the direction in text, beside the arrow and its colour
    expect(tile(host, "4xx rate")).toContain("+0.5 pp");
    expect(tile(host, "5xx rate")).toContain("0.2%");
    expect(tile(host, "5xx rate")).toMatch(/-\d[\d.]* pp/);
    expect(tile(host, "p50")).toContain("42 ms");
    expect(tile(host, "p95")).toContain("310 ms");
    expect(host.textContent).toContain("Compared with");
    // infrastructure requests stay out of the cards and are named apart
    const infra = text(host.querySelector('[data-testid="infrastructure-requests"]'));
    expect(infra).toContain("8,640 health checks and preflights are not included");
    expect(infra).toContain("kept apart since Nov 16, 2026");
  });

  it("shows a notice for each warning kind that needs one, traces only where they apply", async () => {
    serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() =>
      expect(host.querySelector('[data-warning="secret_in_path"]')).not.toBeNull(),
    );
    const kinds = [...host.querySelectorAll("[data-warning]")].map((e) =>
      e.getAttribute("data-warning"),
    );
    expect(kinds).toEqual(["lossy_histogram", "secret_in_path", "coarse_buckets"]);
    expect(text(host.querySelector('[data-warning="secret_in_path"]'))).toContain(
      "last on Nov 11, 2026",
    );
  });

  it("marks trace-derived services and routes with a badge", async () => {
    serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector("[data-route]")).not.toBeNull());
    const services = [...host.querySelectorAll('ul[aria-label="Services"] li')];
    expect(
      services.map((li) => [
        text(li).includes("From traces"),
        li.textContent?.includes("orders-worker"),
      ]),
    ).toEqual([
      [true, true],
      [false, false],
    ]);
    const span = host.querySelector('[data-route="/jobs/{id}"]');
    const metric = host.querySelector('[data-route="/v1/accounts/{id}"]');
    expect(text(span)).toContain("From traces");
    expect(text(metric)).not.toContain("From traces");
    expect(text(span)).toContain(">10 s");
    expect(host.textContent).toContain(
      "Derived from sampled traces; counts are lower than the real request count when traces are sampled.",
    );
  });

  it("says when a service's trace-derived rows were left out (contract 7.3)", async () => {
    const services = {
      ...f.apiServicesBody,
      items: f.apiServicesBody.items.map((i) =>
        i.service === "payments-api" ? { ...i, span_data_ignored: true } : i,
      ),
    };
    serve({ ...full, services });
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector('ul[aria-label="Services"]')).not.toBeNull());
    const items = [...host.querySelectorAll('ul[aria-label="Services"] li')];
    expect(items.map((li) => (li.textContent ?? "").includes("Metrics only"))).toEqual([
      false,
      true,
    ]);
  });

  it("shows the status distribution by class and by code", async () => {
    serve(full);
    const host = await renderRoutes(routes(), path);
    const code = (c: string) =>
      host.querySelector(`ul[aria-label="Status codes"] li[data-key="${c}"]`);
    await waitFor(() => expect(code("503")).not.toBeNull());
    expect(text(code("404"))).toContain("404");
    expect(text(code("404"))).toContain("30");
    expect(text(code("404"))).toContain("2.5%");
    const bar = host.querySelector('[role="img"][aria-label^="Requests by status class"]');
    expect(bar?.getAttribute("aria-label")).toContain("2xx success 1,150");
    // a class without requests draws nothing
    expect(bar?.getAttribute("aria-label")).not.toContain("No status code");
  });

  it("switches the range and asks every route for it", async () => {
    const net = serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector("[data-route]")).not.toBeNull());
    await click(radio(host, "7 days"));
    await waitFor(() => expect(net.last("routes")?.searchParams.get("range")).toBe("7d"));
    for (const k of ["services", "warnings", "overview", "status"])
      expect(net.last(k)?.searchParams.get("range")).toBe("7d");
    // the timeseries is asked twice per range: once for it, and once as a
    // custom range for the previous period the overview names (the dashed line)
    await waitFor(() =>
      expect(
        net.calls.some(
          (u) => u.pathname.endsWith("/timeseries") && u.searchParams.get("range") === "7d",
        ),
      ).toBe(true),
    );
  });

  it("sorts the routes table by a column, then reverses it", async () => {
    const net = serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector("[data-route]")).not.toBeNull());
    expect(net.last("routes")?.search).toBe("?range=24h&sort=requests&order=desc&limit=50");
    await click(header(host, "p95"));
    await waitFor(() => expect(net.last("routes")?.searchParams.get("sort")).toBe("p95"));
    expect(net.last("routes")?.searchParams.get("order")).toBe("desc");
    expect(header(host, "p95").closest("th")?.getAttribute("aria-sort")).toBe("descending");
    await click(header(host, "p95"));
    await waitFor(() => expect(net.last("routes")?.searchParams.get("order")).toBe("asc"));
    await click(header(host, "Route"));
    await waitFor(() => expect(net.last("routes")?.searchParams.get("sort")).toBe("route"));
    expect(net.last("routes")?.searchParams.get("order")).toBe("asc");
  });

  it("pages forward with the cursor and back to the first page", async () => {
    const net = serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("1 to 50 of 134"));
    const next = host.querySelector<HTMLButtonElement>('button[aria-label="Next page"]');
    if (!next) throw new Error("no next button");
    await click(next);
    await waitFor(() =>
      expect(net.last("routes")?.searchParams.get("cursor")).toBe(f.ROUTES_CURSOR),
    );
    await waitFor(() => expect(host.textContent).toContain("51 to 100 of 134"));
    const prev = host.querySelector<HTMLButtonElement>('button[aria-label="Previous page"]');
    if (!prev) throw new Error("no previous button");
    await click(prev);
    await waitFor(() => expect(host.textContent).toContain("1 to 50 of 134"));
  });

  it("filters to a route from its row and clears the filter again", async () => {
    const net = serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector("[data-route]")).not.toBeNull());
    const btn = host.querySelector<HTMLButtonElement>(
      'button[aria-label="Filter to POST /jobs/{id} of orders-worker"]',
    );
    if (!btn) throw new Error("no filter button");
    await click(btn);
    await waitFor(() => expect(net.last("overview")?.searchParams.get("route")).toBe("/jobs/{id}"));
    for (const k of ["overview", "timeseries", "status", "routes"]) {
      const q = net.last(k)?.searchParams;
      expect([q?.get("service"), q?.get("method"), q?.get("route")]).toEqual([
        "orders-worker",
        "POST",
        "/jobs/{id}",
      ]);
    }
    // services and warnings take the range only
    expect(net.last("services")?.searchParams.has("service")).toBe(false);
    const chips = host.querySelector('ul[aria-label="Active filters"]');
    expect(text(chips)).toContain("Route: /jobs/{id}");
    const clear = [...host.querySelectorAll("button")].find((b) => text(b) === "Clear filters");
    if (!clear) throw new Error("no clear button");
    await click(clear);
    // the unfiltered answers are still cached, so nothing is fetched again
    await waitFor(() => expect(host.querySelector('ul[aria-label="Active filters"]')).toBeNull());
    expect(host.querySelector("[data-tile=Requests]")?.textContent).toContain("1,200");
  });

  it("applies a route typed into the filter on enter, with no apply button", async () => {
    const net = serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector("[data-route]")).not.toBeNull());
    const input = byLabel(host, "Route") as HTMLInputElement;
    await type(input, " /v1/accounts/{id} ");
    await act(async () => {
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    await waitFor(() =>
      expect(net.last("routes")?.searchParams.get("route")).toBe("/v1/accounts/{id}"),
    );
  });

  it("refuses a custom range that ends before it starts, and sends a valid one", async () => {
    const net = serve(full);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector("[data-route]")).not.toBeNull());
    await click(radio(host, "Custom"));
    const form = host.querySelector<HTMLFormElement>('form[aria-label="Custom range"]');
    if (!form) throw new Error("no custom range form");
    await type(byLabel(host, "From (UTC)") as HTMLInputElement, "2026-11-05T00:00");
    await type(byLabel(host, "To (UTC)") as HTMLInputElement, "2026-11-04T00:00");
    await act(async () => form.requestSubmit());
    expect(form.textContent).toContain("End after the start.");
    expect(net.last("overview")?.searchParams.get("range")).toBe("24h");
    await type(byLabel(host, "To (UTC)") as HTMLInputElement, "2026-11-06T12:00");
    await act(async () => form.requestSubmit());
    await waitFor(() => expect(net.last("overview")?.searchParams.get("range")).toBe("custom"));
    expect(net.last("overview")?.searchParams.get("from")).toBe("2026-11-05T00:00:00Z");
    expect(net.last("overview")?.searchParams.get("to")).toBe("2026-11-06T12:00:00Z");
  });

  it("lists every bucket in the chart tables, the open bucket as >10 s", async () => {
    serve(full);
    const host = await renderRoutes(routes(), path);
    // the p95 tile drives the chart to latency; its card shows the table
    await waitFor(() =>
      expect(host.querySelector('button[data-tile="p95"][aria-pressed]')).not.toBeNull(),
    );
    const p95 = host.querySelector<HTMLButtonElement>('button[data-tile="p95"][aria-pressed]');
    if (!p95) throw new Error("no p95 card");
    await click(p95);
    await waitFor(() => expect(host.textContent).toContain("Latency per hour"));
    const card = () =>
      [...host.querySelectorAll("section")].find(
        (s) => text(s.querySelector("h2")) === "Latency per hour",
      );
    const toggle = card()?.querySelector<HTMLButtonElement>('button[aria-label="Show as table"]');
    if (!toggle) throw new Error("no table toggle");
    await click(toggle);
    await waitFor(() => expect(card()?.querySelector("tbody tr")).not.toBeNull());
    const rows = [...(card()?.querySelectorAll("tbody tr") ?? [])];
    expect(rows).toHaveLength(24);
    // p95 first, then p50 and p99
    expect(cells(rows[22])).toEqual([
      "Nov 12, 2026, 08:00 UTC, approximate",
      ">10 s",
      "2 s",
      ">10 s",
    ]);
    expect(cells(rows[0])).toEqual(["Nov 11, 2026, 10:00 UTC", "–", "–", "–"]);
  });
});

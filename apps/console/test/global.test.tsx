import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { comparePath, globalListPath } from "@/api/endpoints";
import {
  apiCompare,
  apiKeyCreated,
  apiProjectPage,
  apiSort,
  webCompare,
  webProjectPage,
  webSort,
} from "@/api/schemas";
import {
  EMPTY_FILTERS,
  type ListState,
  lastDays,
  readCompare,
  readListState,
  validateDays,
  writeListState,
} from "@/lib/global";
import { API_COLUMNS, WEB_COLUMNS } from "@/lib/operator-projects";
import { ComparePage, latencyAxis, percentAxis } from "@/pages/operator/compare";
import { blankColumns } from "@/pages/operator/project-columns";
import { OperatorProjectsPage } from "@/pages/operator/projects";
import * as c from "./fixtures/contract";
import * as f from "./fixtures/global";
import { byLabel, byRole, type Call, click, renderRoutes, waitFor } from "./helpers";

const EM_DASH = String.fromCharCode(0x2014);

describe("contract 8 and 9 schemas", () => {
  it("parse the contract's example bodies", () => {
    expect(webProjectPage.safeParse(f.webProjectsBody).success).toBe(true);
    expect(apiProjectPage.safeParse(f.apiProjectsBody).success).toBe(true);
    expect(webCompare.safeParse(f.webCompareBody).success).toBe(true);
    expect(apiCompare.safeParse(f.apiCompareBody).success).toBe(true);
    expect(apiKeyCreated.safeParse(f.apiKeyCreatedBody).success).toBe(true);
  });

  it("refuse a compare whose series do not align with days", () => {
    const bad = structuredClone(f.webCompareBody);
    bad.projects[0]?.series.pop();
    expect(webCompare.safeParse(bad).success).toBe(false);
    const shifted = structuredClone(f.apiCompareBody);
    if (shifted.projects[1]?.series[0]) shifted.projects[1].series[0].day = "2026-11-09";
    expect(apiCompare.safeParse(shifted).success).toBe(false);
  });

  it("refuse a missing field, a key of the wrong shape and an unknown source", () => {
    const { visitors: _, ...noVisitors } = f.webProjectA;
    expect(webProjectPage.safeParse({ ...f.webProjectsBody, items: [noVisitors] }).success).toBe(
      false,
    );
    expect(apiKeyCreated.safeParse({ ...f.apiKeyCreatedBody, key: "sa_op_short" }).success).toBe(
      false,
    );
    expect(
      apiProjectPage.safeParse({
        ...f.apiProjectsBody,
        items: [{ ...f.apiProjectA, source: "both" }],
      }).success,
    ).toBe(false);
  });
});

describe("list state and query strings", () => {
  it("reads every filter of 8.2 from the address and drops malformed values", () => {
    const p = new URLSearchParams(
      "status=active,bogus,suspended&verified=true&stellar_toml=maybe&has_web_data=false&has_api_data=true&enrolled_from=2026-01-01&enrolled_to=not-a-day&q=dex&integrity_at_or_below=B&sort=visitors&order=asc&from=2026-11-01&to=2026-11-12",
    );
    const s = readListState(p, webSort.options, "pageviews");
    expect(s).toEqual({
      range: { from: "2026-11-01", to: "2026-11-12" },
      filters: {
        statuses: ["active", "suspended"],
        verified: "true",
        stellar_toml: "",
        has_web_data: "false",
        has_api_data: "true",
        enrolled_from: "2026-01-01",
        enrolled_to: "",
        q: "dex",
        program: "",
        round: "",
        category: "",
        award_from: "",
        award_to: "",
        integrity: "B",
      },
      sort: "visitors",
      order: "asc",
    });
    expect(
      readListState(new URLSearchParams("sort=visitors"), apiSort.options, "requests").sort,
    ).toBe("requests");
    // only A, B and C are grades
    expect(
      readListState(new URLSearchParams("integrity_at_or_below=D"), webSort.options, "pageviews")
        .filters.integrity,
    ).toBe("");
  });

  it("writes only non-default values and builds the list path with limit and cursor", () => {
    const s: ListState<"pageviews" | "visitors"> = {
      range: { from: "", to: "" },
      filters: { ...EMPTY_FILTERS, verified: "true" },
      sort: "visitors",
      order: "desc",
    };
    expect(writeListState(s, "pageviews").toString()).toBe("verified=true&sort=visitors");
    expect(writeListState({ ...s, sort: "pageviews" }, "pageviews").toString()).toBe(
      "verified=true",
    );
    expect(globalListPath("web", s, "abc")).toBe(
      "/operator/web/projects?verified=true&sort=visitors&order=desc&limit=50&cursor=abc",
    );
  });

  it("reads the comparison's projects: lowercase uuids, distinct, at most five", () => {
    const ids = [
      c.TENANT_ID,
      f.TENANT_B,
      c.TENANT_ID,
      "NOT-A-UUID",
      f.TENANT_C,
      c.SITE_ID,
      c.USER_ID,
      c.INVITE_ID,
    ];
    const r = readCompare(new URLSearchParams(`tenants=${ids.join(",")}&from=2026-11-01&to=bad`));
    expect(r.tenants).toEqual([c.TENANT_ID, f.TENANT_B, f.TENANT_C, c.SITE_ID, c.USER_ID]);
    expect(r.range).toEqual({ from: "2026-11-01", to: "" });
    expect(comparePath("web", [c.TENANT_ID], { from: "2026-11-01", to: "" })).toBe(
      `/operator/web/compare?tenants=${c.TENANT_ID}&from=2026-11-01&breakdown_limit=10`,
    );
    expect(comparePath("api", [c.TENANT_ID, f.TENANT_B], { from: "", to: "" })).toBe(
      `/operator/api/compare?tenants=${encodeURIComponent(`${c.TENANT_ID},${f.TENANT_B}`)}`,
    );
  });

  it("checks custom day ranges against 8.1", () => {
    const now = new Date("2026-11-12T15:00:00Z");
    expect(lastDays(30, now)).toEqual({ from: "2026-10-14", to: "2026-11-12" });
    expect(validateDays({ from: "2026-11-01", to: "2026-11-12" }, now)).toEqual({});
    expect(validateDays({ from: "2026-11-01", to: "2026-11-13" }, now).to).toBeDefined();
    expect(validateDays({ from: "2026-11-05", to: "2026-11-01" }, now).to).toBeDefined();
    expect(validateDays({ from: "2025-11-11", to: "2026-11-12" }, now).to).toContain("366");
    expect(validateDays({ from: "2025-11-12", to: "2026-11-12" }, now)).toEqual({});
  });
});

// answers by method and path, for pages whose queries start together and
// may reach the network in either order. test double only.
function routeFetch(
  answer: (method: string, url: string, n: number) => { status: number; body?: unknown },
): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init: RequestInit = {}) => {
      const method = init.method ?? "GET";
      calls.push({
        url: input,
        method,
        headers: new Headers(init.headers),
        body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
      });
      const a = answer(
        method,
        input,
        calls.filter((c) => c.method === method && c.url === input).length,
      );
      return new Response(a.body === undefined ? null : JSON.stringify(a.body), {
        status: a.status,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }),
  );
  return calls;
}

const operatorRoutes = [
  { path: "/operator/projects", element: <OperatorProjectsPage /> },
  { path: "/operator/compare", element: <ComparePage /> },
];

function rowOf(host: HTMLElement, tenantId: string): HTMLElement {
  const row = host.querySelector<HTMLElement>(`tr[data-tenant="${tenantId}"]`);
  if (!row) throw new Error(`no row for ${tenantId}`);
  return row;
}

// the list of projects answers from its routes, whatever the order
function listFetch(page: unknown) {
  return routeFetch((_m, url) => {
    if (url.startsWith("/api/operator/cohorts")) return { status: 200, body: f.cohortListBody };
    if (
      url.startsWith("/api/operator/web/projects") ||
      url.startsWith("/api/operator/api/projects")
    )
      return { status: 200, body: page };
    return { status: 503, body: c.errorBody("unavailable") };
  });
}

// shows every column of a view, as a person who chose them all
function showAll(view: "web" | "api") {
  const ids = (view === "web" ? WEB_COLUMNS : API_COLUMNS).map((x) => x.id);
  window.localStorage.setItem(`veristar.operator.projects.columns.${view}`, JSON.stringify(ids));
}

function sortHeaders(host: HTMLElement): string[] {
  return [...host.querySelectorAll("table th[aria-sort] button")].map((b) =>
    (b.textContent ?? "").trim(),
  );
}

describe("project list, web view", () => {
  it("shows the default columns, filters at once, sorts by a header and pages", async () => {
    window.localStorage.clear();
    const calls = routeFetch((_m, url) => {
      if (url.startsWith("/api/operator/cohorts")) return { status: 200, body: f.cohortListBody };
      if (url.includes("cursor=")) return { status: 200, body: f.webProjectsPage2Body };
      return { status: 200, body: f.webProjectsBody };
    });
    const host = await renderRoutes(operatorRoutes, "/operator/projects?view=web");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    const a = rowOf(host, c.TENANT_ID);
    // the default columns fit a wide screen: bounce rate and held back are
    // a column choice away
    for (const want of ["1,200", "410", "+100%"]) expect(a.textContent).toContain(want);
    expect(a.textContent).not.toContain("49%");
    // a project without a previous range shows the missing glyph with its reason
    expect(rowOf(host, f.TENANT_B).textContent).toContain("No value in the previous range");
    const visitorsHead = [...host.querySelectorAll("th button")].find(
      (b) => b.textContent === "Visitors",
    );
    expect(visitorsHead?.getAttribute("aria-description")).toContain("unique per UTC day");
    const list = () => calls.filter((x) => x.url.startsWith("/api/operator/web/projects"));
    expect(list()[0]?.url).toBe("/api/operator/web/projects?sort=pageviews&order=desc&limit=50");

    // a filter in the filter sheet applies at once
    const verified = byLabel(host, "Domain verified") as unknown as HTMLSelectElement;
    await act(async () => {
      verified.value = "true";
      verified.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await waitFor(() =>
      expect(list().map((x) => x.url)).toContain(
        "/api/operator/web/projects?verified=true&sort=pageviews&order=desc&limit=50",
      ),
    );

    // sort by visitors from the column header
    const header = [...host.querySelectorAll("th button")].find(
      (b) => b.textContent === "Visitors",
    );
    if (!header) throw new Error("no visitors header");
    await click(header as HTMLElement);
    await waitFor(() =>
      expect(list().map((x) => x.url)).toContain(
        "/api/operator/web/projects?verified=true&sort=visitors&order=desc&limit=50",
      ),
    );
    expect(header.closest("th")?.getAttribute("aria-sort")).toBe("descending");

    // a page of 25 the first rows do not fill loads the next rows by itself
    await waitFor(() => expect(host.textContent).toContain("Anchor Hub"));
    expect(host.querySelector("nav[aria-label=Pages]")?.textContent).toContain("1 to 3 of 3");
    expect(list().some((x) => x.url.includes("cursor=cG9zOjI"))).toBe(true);
  });

  it("leaves out by default a column with no value on any row of the page", async () => {
    const rows = [f.webProjectA, f.webProjectB] as unknown as Parameters<typeof blankColumns>[0];
    const blank = blankColumns(rows);
    // project A has a visitors change, nobody has an on-chain change
    expect(blank.has("visitors_growth")).toBe(false);
    expect(blank.has("onchain_operations_growth")).toBe(true);
    // a cohort of 4 shows no percentile
    expect(blank.has("visitors_percentile")).toBe(true);
    expect(blankColumns([]).size).toBe(0);
  });

  it("reaches every web sort key of 8.3 as a sortable column", async () => {
    showAll("web");
    listFetch(f.webProjectsBody);
    const host = await renderRoutes(operatorRoutes, "/operator/projects?view=web");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    expect(sortHeaders(host)).toHaveLength(webSort.options.length);
    expect(
      WEB_COLUMNS.filter((x) => x.sortKey)
        .map((x) => x.sortKey)
        .sort(),
    ).toEqual([...webSort.options].sort());
    const a = rowOf(host, c.TENANT_ID);
    for (const want of ["530", "1 min 31 s", "4.0%", "Germany 120"])
      expect(a.textContent).toContain(want);
    window.localStorage.clear();
  });

  it("selects projects into a comparison bar that shows only with a selection", async () => {
    window.localStorage.clear();
    listFetch(f.webProjectsBody);
    const host = await renderRoutes(operatorRoutes, "/operator/projects?view=web");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    expect(host.querySelector("[aria-label='Selected projects']")).toBeNull();
    for (const id of [c.TENANT_ID, f.TENANT_B]) {
      const box = rowOf(host, id).querySelector<HTMLElement>("[role=checkbox]");
      if (!box) throw new Error("no checkbox");
      await click(box);
    }
    expect(host.querySelector("[aria-label='Selected projects']")?.textContent).toContain(
      "2 selected",
    );
    const link = [...host.querySelectorAll("a")].find((x) => x.textContent === "Open in Compare");
    expect(link?.getAttribute("href")).toBe(
      `/operator/compare?tenants=${encodeURIComponent(`${c.TENANT_ID},${f.TENANT_B}`)}&from=2026-10-14&to=2026-11-12`,
    );
  });
});

describe("project list, portfolio", () => {
  it("counts the portfolio from the list totals and filters the list in one click", async () => {
    window.localStorage.clear();
    const calls = routeFetch((_m, url) => {
      if (url.startsWith("/api/operator/cohorts")) return { status: 200, body: f.cohortListBody };
      if (url.startsWith("/api/operator/tenants"))
        return {
          status: 200,
          body: { items: [c.operatorSummaryBody], next_cursor: null },
        };
      if (url.startsWith("/api/operator/web/projects"))
        return {
          status: 200,
          body: { ...f.webProjectsBody, total: url.includes("has_web_data=false") ? 7 : 3 },
        };
      return { status: 503, body: c.errorBody("unavailable") };
    });
    // the web view is the default
    const host = await renderRoutes(operatorRoutes, "/operator/projects");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    const none = () => host.querySelector<HTMLElement>("[data-segment=none]") as HTMLElement;
    await waitFor(() => expect(none().textContent).toBe("7No data"));
    // in review is a status, not a second count
    expect(host.querySelector("[data-segment=review]")).toBeNull();
    expect(host.querySelector("[data-segment=all]")?.getAttribute("aria-pressed")).toBe("true");
    await click(none());
    await waitFor(() =>
      expect(
        calls.some(
          (x) =>
            x.url.includes("has_web_data=false&has_api_data=false") &&
            x.url.includes("sort=pageviews"),
        ),
      ).toBe(true),
    );
    await waitFor(() => expect(none().getAttribute("aria-pressed")).toBe("true"));
  });
});

describe("project list, api view", () => {
  it("shows requests, rates, percentiles with >10 s, services, routes and the source", async () => {
    showAll("api");
    const calls = listFetch(f.apiProjectsBody);
    const host = await renderRoutes(operatorRoutes, "/operator/projects?view=api");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    const a = rowOf(host, c.TENANT_ID);
    for (const want of [
      "182,000",
      "1.2%",
      "<0.1%",
      "41 ms",
      "620 ms",
      ">10 s",
      "41",
      "Metrics and traces (1 of 3 services from traces)",
    ])
      expect(a.textContent).toContain(want);
    expect(rowOf(host, f.TENANT_B).textContent).toContain("No requests in this range");
    expect(host.textContent).toContain("Traces are sampled");
    expect(sortHeaders(host)).toHaveLength(apiSort.options.length);
    expect(
      API_COLUMNS.filter((x) => x.sortKey)
        .map((x) => x.sortKey)
        .sort(),
    ).toEqual([...apiSort.options].sort());
    expect(calls.find((x) => x.url.startsWith("/api/operator/api/projects"))?.url).toBe(
      "/api/operator/api/projects?sort=requests&order=desc&limit=50",
    );
    window.localStorage.clear();
  });
});

// the cells of one metric column of a totals table, top to bottom

// the cells of one scorecard row, one per compared project
function rowCells(host: HTMLElement, row: string): string[] {
  return [...host.querySelectorAll(`tr[data-row="${row}"] td`)].map((td) => td.textContent ?? "");
}

function compareFetch() {
  return routeFetch((_m, url) => {
    if (url.startsWith("/api/operator/web/compare")) return { status: 200, body: f.webCompareBody };
    if (url.startsWith("/api/operator/api/compare")) return { status: 200, body: f.apiCompareBody };
    if (url.startsWith("/api/operator/cohorts")) return { status: 200, body: f.cohortListBody };
    if (url.startsWith("/api/operator/web/projects"))
      return { status: 200, body: f.webProjectsBody };
    return { status: 503, body: c.errorBody("unavailable") };
  });
}

describe("comparison", () => {
  it("shows each compared project as a scorecard column with web and api rows", async () => {
    const calls = compareFetch();
    const host = await renderRoutes(
      operatorRoutes,
      `/operator/compare?tenants=${c.TENANT_ID},${f.TENANT_B}`,
    );
    await waitFor(() => expect(host.querySelector("tr[data-row=visitors]")).not.toBeNull());
    const urls = calls.map((x) => x.url);
    expect(urls).toContain(
      `/api/operator/web/compare?tenants=${encodeURIComponent(`${c.TENANT_ID},${f.TENANT_B}`)}&breakdown_limit=10`,
    );
    expect(urls).toContain(
      `/api/operator/api/compare?tenants=${encodeURIComponent(`${c.TENANT_ID},${f.TENANT_B}`)}`,
    );
    // the time is when the aggregation read the numbers, not when they arrived
    expect(host.querySelector("[data-freshness]")?.getAttribute("datetime")).toBe(
      "2026-11-12T14:05:00Z",
    );
    // the change against the previous period follows the count: 18 from 20
    const visitors = rowCells(host, "visitors");
    expect(visitors[0]).toContain("18");
    expect(visitors[0]).toContain("10%");
    // a project without web data says so in its cells
    expect(visitors[1]).toContain("No data");
    expect(rowCells(host, "requests")[0]).toContain("300");
    expect(rowCells(host, "p99").join(" ")).toContain(">10 s");
    // the breakdowns and the services open from their group
    await click(byRole(host, "button", "Referrers, countries, devices"));
    await waitFor(() => expect(host.textContent).toContain("github.com"));
    expect(host.textContent).toContain("13 more");
    expect(host.textContent).toContain("Germany");
    await click(host.querySelector("dialog[open] button[aria-label=Close]") as HTMLElement);
    await click(byRole(host, "button", "Services of each project"));
    await waitFor(() => expect(host.textContent).toContain("payments-api"));
    expect(host.textContent).toContain("from traces");
    // a row draws its chart above the table
    await click(byRole(host, "button", "Verified visitors"));
    expect(host.querySelector("section[aria-label='Verified visitors']")).not.toBeNull();
  });

  it("keeps the compared projects and the range when an operator adds a project", async () => {
    const calls = compareFetch();
    const host = await renderRoutes(
      operatorRoutes,
      `/operator/compare?tenants=${c.TENANT_ID},${f.TENANT_B}`,
    );
    await waitFor(() => expect(host.querySelector("tr[data-row=visitors]")).not.toBeNull());
    await click(byRole(host, "button", "Add a project"));
    await waitFor(() => expect(host.textContent).toContain("2 of 5 picked"));
    const list = calls.find((x) => x.url.startsWith("/api/operator/web/projects"))?.url ?? "";
    const range = f.webCompareBody as { from: string; to: string };
    expect(list).toContain(`from=${range.from}`);
    expect(list).toContain(`to=${range.to}`);
  });

  it("offers the picker when no project is named", async () => {
    compareFetch();
    const host = await renderRoutes(operatorRoutes, "/operator/compare");
    await waitFor(() => expect(host.textContent).toContain("Pick up to 5 projects"));
    await waitFor(() => expect(host.textContent).toContain("0 of 5 picked"));
  });
});

describe("comparison axes", () => {
  it("keep the decimals a rate step needs", () => {
    const axis = percentAxis([[0.009, null], [0.002]]);
    expect([0, 0.0025, 0.005, 0.0075, 0.01].map(axis)).toEqual([
      "0%",
      "0.25%",
      "0.50%",
      "0.75%",
      "1.00%",
    ]);
    expect([0, 0.25, 0.5].map(percentAxis([[0.44]]))).toEqual(["0%", "25%", "50%"]);
  });

  it("draw latency in one unit that fits the values", () => {
    const ms = latencyAxis([[0.342], [0.739, null]]);
    expect([0, 0.2, 0.8].map(ms)).toEqual(["0 ms", "200 ms", "800 ms"]);
    const s = latencyAxis([[2.25]]);
    expect([0, 0.5, 2.5].map(s)).toEqual(["0 s", "0.5 s", "2.5 s"]);
  });
});

describe("copy", () => {
  it("has no em dash in the new pages", async () => {
    compareFetch();
    const host = await renderRoutes(
      operatorRoutes,
      `/operator/compare?tenants=${c.TENANT_ID},${f.TENANT_B}`,
    );
    await waitFor(() => expect(host.querySelector("tr[data-row=visitors]")).not.toBeNull());
    expect(host.textContent).not.toContain(EM_DASH);
  });
});

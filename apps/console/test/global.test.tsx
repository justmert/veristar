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
  formatDuration,
  formatPct,
  type ListState,
  lastDays,
  readCompare,
  readListState,
  validateDays,
  writeListState,
} from "@/lib/global";
import { ApiKeysPage, validateKeyForm } from "@/pages/operator/api-keys";
import { ComparePage } from "@/pages/operator/compare";
import { GlobalApiPage } from "@/pages/operator/global-api";
import { GlobalWebPage } from "@/pages/operator/global-web";
import * as c from "./fixtures/contract";
import * as f from "./fixtures/global";
import {
  byLabel,
  byRole,
  type Call,
  click,
  renderRoutes,
  stubFetch,
  type,
  waitFor,
} from "./helpers";

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

  it("formats durations and percentiles, never inventing a number above 10 s", () => {
    expect(formatDuration(90.94)).toBe("1 min 31 s");
    expect(formatDuration(null)).toBeNull();
    expect(formatPct(null, true)).toBe(">10 s");
    expect(formatPct(0.041, false)).toBe("41 ms");
    expect(formatPct(null, false)).toBeNull();
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
  { path: "/operator/web", element: <GlobalWebPage /> },
  { path: "/operator/api", element: <GlobalApiPage /> },
  { path: "/operator/compare", element: <ComparePage /> },
  { path: "/operator/api-keys", element: <ApiKeysPage /> },
];

function rowOf(host: HTMLElement, tenantId: string): HTMLElement {
  const row = host.querySelector<HTMLElement>(`tr[data-tenant="${tenantId}"]`);
  if (!row) throw new Error(`no row for ${tenantId}`);
  return row;
}

describe("global web view", () => {
  it("shows every column of 8.3, sends filters and sorts, and pages with the cursor", async () => {
    const calls = stubFetch([
      { status: 200, body: f.cohortListBody },
      { status: 200, body: f.webProjectsBody },
      { status: 200, body: f.webProjectsBody },
      { status: 200, body: f.webProjectsBody },
      { status: 200, body: f.webProjectsPage2Body },
    ]);
    const host = await renderRoutes(operatorRoutes, "/operator/web");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    const a = rowOf(host, c.TENANT_ID);
    for (const want of ["1,200", "410", "530", "49.1%", "1 min 31 s", "3.2%", "4%", "Germany (DE)"])
      expect(a.textContent).toContain(want);
    // a project without visits has no ratios
    expect(rowOf(host, f.TENANT_B).textContent).toContain("none");
    expect(host.textContent).toContain("IP Geolocation by DB-IP");
    expect(host.textContent).toContain("unique per UTC day");
    expect(calls[1]?.url).toBe("/api/operator/web/projects?sort=pageviews&order=desc&limit=50");

    // filter to verified projects
    const verified = byLabel(host, "Domain verified") as unknown as HTMLSelectElement;
    await act(async () => {
      verified.value = "true";
      verified.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await waitFor(() =>
      expect(calls[2]?.url).toBe(
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
      expect(calls[3]?.url).toBe(
        "/api/operator/web/projects?verified=true&sort=visitors&order=desc&limit=50",
      ),
    );
    expect(header.closest("th")?.getAttribute("aria-sort")).toBe("descending");

    await click(byRole(host, "button", "Load more"));
    await waitFor(() => expect(host.textContent).toContain("Anchor Hub"));
    expect(calls[4]?.url).toContain("cursor=cG9zOjI");
  });

  it("selects up to five projects into a comparison link", async () => {
    stubFetch([
      { status: 200, body: f.cohortListBody },
      { status: 200, body: f.webProjectsBody },
    ]);
    const host = await renderRoutes(operatorRoutes, "/operator/web");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    for (const id of [c.TENANT_ID, f.TENANT_B]) {
      const box = rowOf(host, id).querySelector<HTMLElement>("[role=checkbox]");
      if (!box) throw new Error("no checkbox");
      await click(box);
    }
    const link = [...host.querySelectorAll("a")].find((a) => a.textContent === "Compare");
    expect(link?.getAttribute("href")).toBe(
      `/operator/compare?tenants=${encodeURIComponent(`${c.TENANT_ID},${f.TENANT_B}`)}&from=2026-10-14&to=2026-11-12`,
    );
  });
});

describe("global api view", () => {
  it("shows requests, rates, percentiles with >10 s, services, routes and the source", async () => {
    const calls = stubFetch([
      { status: 200, body: f.cohortListBody },
      { status: 200, body: f.apiProjectsBody },
    ]);
    const host = await renderRoutes(operatorRoutes, "/operator/api");
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    const a = rowOf(host, c.TENANT_ID);
    for (const want of [
      "182,000",
      "1.15%",
      "0.05%",
      "41 ms",
      "620 ms",
      ">10 s",
      "41",
      "Metrics and traces (1 of 3 from traces)",
    ])
      expect(a.textContent).toContain(want);
    expect(rowOf(host, f.TENANT_B).textContent).toContain("none");
    // the requests left out as infrastructure are shown, not hidden in a title
    expect(a.textContent).toContain("+0 infrastructure");
    expect(a.querySelector("[title*='infrastructure']")).toBeNull();
    expect(host.textContent).toContain("Derived from sampled traces");
    expect(calls[1]?.url).toBe("/api/operator/api/projects?sort=requests&order=desc&limit=50");
  });
});

describe("comparison", () => {
  it("aligns web and api numbers of the compared projects and marks unread days", async () => {
    const calls = stubFetch([
      { status: 200, body: f.webCompareBody },
      { status: 200, body: f.apiCompareBody },
    ]);
    const host = await renderRoutes(
      operatorRoutes,
      `/operator/compare?tenants=${c.TENANT_ID},${f.TENANT_B}`,
    );
    await waitFor(() => expect(host.textContent).toContain("payments-api"));
    expect(calls.map((x) => x.url)).toEqual([
      `/api/operator/web/compare?tenants=${encodeURIComponent(`${c.TENANT_ID},${f.TENANT_B}`)}&breakdown_limit=10`,
      `/api/operator/api/compare?tenants=${encodeURIComponent(`${c.TENANT_ID},${f.TENANT_B}`)}`,
    ]);
    // one row per compared project, one cell per metric
    const web = host.querySelector("section[aria-label='Web totals']");
    const visitors = [...(web?.querySelectorAll("td[data-metric='Visitors']") ?? [])];
    expect(visitors.map((td) => td.textContent)).toEqual(["18", "0"]);
    const api = host.querySelector("section[aria-label='API totals']");
    const p99 = [...(api?.querySelectorAll("td[data-metric='p99']") ?? [])];
    expect(p99.map((td) => td.textContent).join(" ")).toContain(">10 s");
    expect(host.textContent).toContain("not read");
    expect(host.textContent).toContain("github.com");
    expect(host.textContent).toContain("13 more");
    expect(host.textContent).toContain("Germany (DE)");
    expect(host.textContent).toContain("IP Geolocation by DB-IP");
    expect(host.textContent).toContain("from traces");
  });

  it("keeps the compared projects and the range when an operator adds a project", async () => {
    const calls = routeFetch((_m, url) => {
      if (url.startsWith("/api/operator/web/compare"))
        return { status: 200, body: f.webCompareBody };
      if (url.startsWith("/api/operator/api/compare"))
        return { status: 200, body: f.apiCompareBody };
      if (url.startsWith("/api/operator/cohorts")) return { status: 200, body: f.cohortListBody };
      if (url.startsWith("/api/operator/web/projects"))
        return { status: 200, body: f.webProjectsBody };
      return { status: 503, body: c.errorBody("unavailable") };
    });
    const host = await renderRoutes(
      operatorRoutes,
      `/operator/compare?tenants=${c.TENANT_ID},${f.TENANT_B}`,
    );
    await waitFor(() => expect(host.textContent).toContain("payments-api"));
    const add = [...host.querySelectorAll("a")].find((a) => a.textContent === "Add a project");
    if (!add) throw new Error("no add link");
    await click(add);
    await waitFor(() => expect(host.textContent).toContain("2 of 5 selected"));
    const selected = host.querySelector("[aria-label='Selected projects']")?.textContent ?? "";
    expect(selected).toContain("Stellar DEX");
    const list = calls.find((x) => x.url.startsWith("/api/operator/web/projects"))?.url ?? "";
    const range = f.webCompareBody as { from: string; to: string };
    expect(list).toContain(`from=${range.from}`);
    expect(list).toContain(`to=${range.to}`);
  });

  it("asks for a selection when no project is named", async () => {
    const calls = stubFetch([]);
    const host = await renderRoutes(operatorRoutes, "/operator/compare");
    await waitFor(() => expect(host.textContent).toContain("No project is selected"));
    expect(calls).toEqual([]);
  });
});

describe("api keys", () => {
  it("validates the create form with the rules of 9.2", () => {
    expect(
      validateKeyForm({ name: " ", scope: "all", tenantId: "", days: "90" }).errors.name,
    ).toBeDefined();
    expect(
      validateKeyForm({ name: "a", scope: "tenant", tenantId: "", days: "90" }).errors.tenantId,
    ).toBeDefined();
    expect(
      validateKeyForm({ name: "a", scope: "all", tenantId: "", days: "366" }).errors.days,
    ).toBeDefined();
    expect(
      validateKeyForm({ name: "a\u0007", scope: "all", tenantId: "", days: "1" }).errors.name,
    ).toBeDefined();
    expect(
      validateKeyForm({ name: " warehouse ", scope: "tenant", tenantId: c.TENANT_ID, days: "30" })
        .body,
    ).toEqual({
      name: "warehouse",
      scope: "metrics:read",
      tenant_id: c.TENANT_ID,
      expires_in_days: 30,
    });
  });

  it("creates a key, shows it once with a copy button, and revokes after a confirmation", async () => {
    let revoked = false;
    let created = false;
    const calls = routeFetch((method, url) => {
      if (url.startsWith("/api/operator/tenants?"))
        return { status: 200, body: { items: [c.operatorSummaryBody], next_cursor: null } };
      if (method === "POST") {
        created = true;
        return { status: 201, body: f.apiKeyCreatedBody };
      }
      if (method === "DELETE") {
        revoked = true;
        return { status: 204 };
      }
      if (url === "/api/operator/api-keys?limit=200")
        return {
          status: 200,
          body: revoked
            ? { items: [f.revokedKeyBody], next_cursor: null }
            : created
              ? f.apiKeyPageBody
              : { items: [], next_cursor: null },
        };
      return { status: 500 };
    });
    const host = await renderRoutes(operatorRoutes, "/operator/api-keys");
    await waitFor(() => expect(host.textContent).toContain("No key has been created yet."));
    expect(
      calls.some((x) => x.url.includes("status=active%2Cpending%2Csuspended%2Crejected")),
    ).toBe(true);

    await type(byLabel(host, "Name") as HTMLInputElement, "warehouse");
    await click(byRole(host, "button", "Create key"));
    // the one-time secret is masked until it is revealed
    await waitFor(() => byRole(host, "button", "Copy key"));
    expect(host.querySelector('[aria-label="Secret hidden"]')).toBeTruthy();
    await click(host.querySelector('[aria-label="Show the secret"]') as HTMLElement);
    await waitFor(() => expect(host.textContent).toContain(f.KEY_PLAIN));
    const post = calls.find((x) => x.method === "POST");
    expect(post).toMatchObject({
      method: "POST",
      url: "/api/operator/api-keys",
      body: { name: "warehouse", scope: "metrics:read", expires_in_days: 90 },
    });
    expect(post?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(byRole(host, "button", "Copy key")).toBeTruthy();

    // once dismissed, the key is not shown again
    // done stays disabled until the operator says the key is stored
    expect(byRole(host, "button", "Done").hasAttribute("disabled")).toBe(true);
    await click(host.querySelector('[role="checkbox"]') as HTMLElement);
    await click(byRole(host, "button", "Done"));
    await waitFor(() => expect(host.textContent).toContain("sa_op_Ab3x..."));
    expect(host.textContent).not.toContain(f.KEY_PLAIN);

    await click(byRole(host, "button", "Revoke"));
    const dialog = [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
    expect(dialog?.textContent).toContain("next request with this key is refused");
    const confirm = [...(dialog?.querySelectorAll("button") ?? [])].find(
      (b) => b.textContent === "Revoke key",
    );
    if (!confirm) throw new Error("no confirm button");
    await click(confirm);
    await waitFor(() => expect(host.textContent).toContain("Revoked"));
    expect(calls.find((x) => x.method === "DELETE")?.url).toBe(
      `/api/operator/api-keys/${f.KEY_ID}`,
    );
  });

  it("shows usage per key from 9.11", async () => {
    const calls = routeFetch((_m, url) => {
      if (url.startsWith("/api/operator/tenants?"))
        return { status: 200, body: { items: [], next_cursor: null } };
      if (url === "/api/operator/api-keys?limit=200")
        return { status: 200, body: f.apiKeyPageBody };
      if (url.endsWith("/usage")) return { status: 200, body: f.apiKeyUsageBody };
      return { status: 500 };
    });
    const host = await renderRoutes(operatorRoutes, "/operator/api-keys");
    await waitFor(() => expect(host.textContent).toContain("sa_op_Ab3x..."));
    await click(byRole(host, "button", "Usage"));
    await waitFor(() =>
      expect(host.querySelector("[data-usage=requests]")?.textContent).toBe("12"),
    );
    expect(host.querySelector("[data-usage=rejected]")?.textContent).toBe("2");
    expect(calls.some((x) => x.url === `/api/operator/api-keys/${f.KEY_ID}/usage`)).toBe(true);
  });
});

describe("copy", () => {
  it("has no em dash in the new pages", async () => {
    stubFetch([
      { status: 200, body: f.webCompareBody },
      { status: 200, body: f.apiCompareBody },
    ]);
    const host = await renderRoutes(
      operatorRoutes,
      `/operator/compare?tenants=${c.TENANT_ID},${f.TENANT_B}`,
    );
    await waitFor(() => expect(host.textContent).toContain("payments-api"));
    expect(host.textContent).not.toContain(EM_DASH);
  });
});

// api traffic dashboard, end to end: a tenant creates a token, two copies of the go
// example app send real requests' telemetry through the edge caddy, the
// collector, nats and the metricswriter (one exports metrics and traces,
// one traces only), and a second tenant's app sends traffic of its own. in
// the console the tenant sees exactly its own endpoints with the numbers
// that were sent, switches ranges and filters, sorts the routes table, and
// sees the trace-derived badge on the traces-only service. nothing here
// answers for a product service; the numbers come from the requests below.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { BrowserContext, Page } from "@playwright/test";
import { APP, APPS_CTL, OUT } from "../fixture/env.ts";
import { api, enrollApi, newUser, signIn, type Tenant } from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const shots = join(OUT, "api-traffic");

// service names carry a run suffix, so a retried run never meets an earlier one
const run = Math.random().toString(36).slice(2, 7);
const PAYMENTS = `payments-api-${run}`;
const LEDGER = `ledger-worker-${run}`;
const FOREIGN = `foreign-api-${run}`;

type Key = string; // service|method|route|status
const key = (service: string, method: string, route: string, status: number): Key =>
  `${service}|${method}|${route}|${status}`;

interface RouteItem {
  service: string;
  method: string;
  route: string;
  source: string;
  requests: number;
  errors_4xx: number;
  errors_5xx: number;
}
interface RoutesBody {
  total: number;
  items: RouteItem[];
  next_cursor: string | null;
}
interface ServicesBody {
  items: { service: string; source: string; requests: number }[];
}

let ctx: BrowserContext;
let page: Page;
let tenant: Tenant;
let foreignId = "";
const problems: string[] = [];
const sent = new Map<Key, number>();

async function token(c: BrowserContext, tenantId: string, name: string): Promise<string> {
  const r = await api<{ token: string }>(c, "POST", `/api/tenants/${tenantId}/otlp-tokens`, {
    name,
  });
  expect(r.status, JSON.stringify(r.body)).toBe(201);
  return r.body.token;
}

async function startApp(service: string, tok: string, signals: string): Promise<string> {
  const r = await fetch(`${APPS_CTL}/apps`, {
    method: "POST",
    body: JSON.stringify({ service, token: tok, signals }),
  });
  const text = await r.text();
  expect(r.status, text).toBe(200);
  return (JSON.parse(text) as { url: string }).url;
}

// one real request to an example app, counted under its route template
async function call(
  service: string,
  base: string,
  method: string,
  path: string,
  route: string,
  body: unknown,
  status: number,
): Promise<unknown> {
  const res = await fetch(`${base}${path}`, {
    method,
    headers: body === undefined ? {} : { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  expect(res.status, `${method} ${path}: ${text}`).toBe(status);
  const k = key(service, method, route, status);
  sent.set(k, (sent.get(k) ?? 0) + 1);
  return text ? JSON.parse(text) : null;
}

// a mix of successes, client errors and every route of the example
async function drive(service: string, base: string, scale: number): Promise<void> {
  const ids: string[] = [];
  for (let i = 0; i < 3 * scale; i++) {
    const a = (await call(
      service,
      base,
      "POST",
      "/accounts",
      "/accounts",
      { name: `a${i}` },
      201,
    )) as {
      id: string;
    };
    ids.push(a.id);
  }
  for (let i = 0; i < scale; i++)
    await call(service, base, "POST", "/accounts", "/accounts", { name: "" }, 400);
  for (const id of ids)
    await call(service, base, "GET", `/accounts/${id}`, "/accounts/{id}", undefined, 200);
  for (let i = 0; i < 2 * scale; i++)
    await call(service, base, "GET", `/accounts/${9000 + i}`, "/accounts/{id}", undefined, 404);
  for (const id of ids.slice(0, scale))
    await call(
      service,
      base,
      "POST",
      `/accounts/${id}/payments`,
      "/accounts/{id}/payments",
      { amount: "1.5" },
      201,
    );
  for (const id of ids.slice(0, scale))
    await call(
      service,
      base,
      "GET",
      `/accounts/${id}/payments`,
      "/accounts/{id}/payments",
      undefined,
      200,
    );
}

function sentFor(service: string): number {
  let n = 0;
  for (const [k, v] of sent) if (k.startsWith(`${service}|`)) n += v;
  return n;
}

// requests per service, method and route, as the routes table counts them
function expectedRoutes(services: string[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const [k, v] of sent) {
    const [service, method, route] = k.split("|");
    if (!services.includes(service ?? "")) continue;
    const r = `${service}|${method}|${route}`;
    out.set(r, (out.get(r) ?? 0) + v);
  }
  return out;
}

async function routesOf(
  c: BrowserContext,
  tenantId: string,
  range: string,
): Promise<Map<string, RouteItem>> {
  const r = await api<RoutesBody>(
    c,
    "GET",
    `/api/tenants/${tenantId}/api-metrics/routes?range=${range}&limit=200`,
  );
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  return new Map(r.body.items.map((i) => [`${i.service}|${i.method}|${i.route}`, i]));
}

// waits until the api answers exactly what was sent for these services:
// the apps export every 3 s, the writer applies batches as they arrive,
// and hourly rows follow with the rollup every 5 minutes
async function waitExact(
  c: BrowserContext,
  tenantId: string,
  services: string[],
  range: string,
  timeout: number,
) {
  const want = expectedRoutes(services);
  await expect
    .poll(
      async () => {
        const got = await routesOf(c, tenantId, range);
        return [...want.keys()].map((k) => [k, got.get(k)?.requests ?? 0]);
      },
      { timeout, intervals: [1000, 2000, 5000] },
    )
    .toEqual([...want.entries()]);
}

async function rows(p: Page) {
  return p.locator("tbody tr[data-route]").evaluateAll((trs) =>
    trs.map((tr) => {
      // the requests cell is found by its column header, not by position
      const heads = [...(tr.closest("table")?.querySelectorAll("thead th") ?? [])];
      const at = heads.findIndex((th) => th.textContent?.trim() === "Requests");
      return {
        service: tr.getAttribute("data-service") ?? "",
        method: tr.getAttribute("data-method") ?? "",
        route: tr.getAttribute("data-route") ?? "",
        requests: Number((tr.children[at]?.textContent ?? "").replace(/,/g, "")),
        text: tr.textContent ?? "",
      };
    }),
  );
}

// the range control is a radio group with the presets' short names
// the range is a radio group on a wide screen and a select on a phone
async function pickRange(p: Page, label: string) {
  const select = p.getByRole("combobox", { name: "Range" });
  if (await select.isVisible()) {
    await select.selectOption({ label });
    return;
  }
  await p
    .getByRole("radiogroup", { name: "Range" })
    .getByRole("radio", { name: label, exact: true })
    .click();
}

// the value of the requests tile, exactly as shown (it counts to a new value
// on change, so the expectation retries until it settles)
function requestsValue(p: Page, n: number) {
  return p.locator('[data-tile="Requests"]').getByText(n.toLocaleString("en"), { exact: true });
}

test.beforeAll(async ({ browser }) => {
  test.setTimeout(300_000);
  mkdirSync(shots, { recursive: true });
  ctx = await browser.newContext();
  page = await ctx.newPage();
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
  await signIn(page, newUser("apitraffic"));
  tenant = await enrollApi(ctx, `API traffic ${run}`);
  expect(tenant.status, "the stack enrolls in open mode").toBe("active");

  // a second tenant with traffic of its own, which the first must never see
  const other = await browser.newContext();
  const otherPage = await other.newPage();
  await signIn(otherPage, newUser("apiforeign"));
  const foreign = await enrollApi(other, `Foreign ${run}`);
  foreignId = foreign.id;
  const foreignBase = await startApp(
    FOREIGN,
    await token(other, foreign.id, "foreign"),
    "metrics,traces",
  );
  await drive(FOREIGN, foreignBase, 1);

  const tok = await token(ctx, tenant.id, "e2e apps");
  const payments = await startApp(PAYMENTS, tok, "metrics,traces");
  const ledger = await startApp(LEDGER, tok, "traces");
  await drive(PAYMENTS, payments, 3);
  await drive(LEDGER, ledger, 1);

  await waitExact(ctx, tenant.id, [PAYMENTS, LEDGER], "1h", 120_000);
  await waitExact(other, foreign.id, [FOREIGN], "1h", 120_000);
  await other.close();
});

test.afterAll(async () => {
  await ctx?.close();
});

test("the tenant sees its own endpoints with the numbers that were sent", async () => {
  await page.goto(`${APP}/projects/${tenant.id}`);
  await page
    .getByRole("navigation", { name: "Project" })
    .getByRole("link", { name: "API", exact: true })
    .click();
  await page.waitForURL(`${APP}/projects/${tenant.id}/api`);
  // the sidebar marks the api page as current
  await expect(
    page
      .getByRole("navigation", { name: "Project" })
      .getByRole("link", { name: "API", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  // exact: the project is named "API traffic <run>"
  await expect(page.getByRole("heading", { name: "API", exact: true })).toBeVisible();

  await pickRange(page, "1 hour");
  await page.waitForURL(/range=1h/);
  await expect(requestsValue(page, sentFor(PAYMENTS) + sentFor(LEDGER))).toBeVisible();

  const services = page.getByRole("list", { name: "Services" });
  await expect(services.getByRole("listitem")).toHaveCount(2);
  await expect(services).toContainText(PAYMENTS);
  await expect(services).toContainText(LEDGER);
  await expect(page.getByText(FOREIGN)).toHaveCount(0);

  const want = expectedRoutes([PAYMENTS, LEDGER]);
  await expect.poll(async () => (await rows(page)).length).toBe(want.size);
  const shown = await rows(page);
  expect(new Map(shown.map((r) => [`${r.service}|${r.method}|${r.route}`, r.requests]))).toEqual(
    want,
  );
  expect(shown.every((r) => !r.text.includes(FOREIGN))).toBe(true);

  // the status distribution counts each code that was answered: the success
  // codes and the errors by code, both inside the status codes block
  const codes = new Map<number, number>();
  for (const [k, v] of sent) {
    const [service, , , status] = k.split("|");
    if (service === FOREIGN) continue;
    codes.set(Number(status), (codes.get(Number(status)) ?? 0) + v);
  }
  for (const [code, n] of codes)
    await expect(
      page.getByRole("region", { name: "Status codes" }).locator(`li[data-key="${code}"]`),
    ).toContainText(n.toLocaleString("en"));

  // the other tenant's endpoints answer 404, as every tenant route does
  for (const kind of ["overview", "timeseries", "routes", "status", "services", "warnings"]) {
    const r = await api(ctx, "GET", `/api/tenants/${foreignId}/api-metrics/${kind}?range=1h`);
    expect(r.status, kind).toBe(404);
  }
  await page.screenshot({ path: join(shots, "own-endpoints.png"), fullPage: true });
});

test("the traces-only service carries the trace-derived badge", async () => {
  const services = page.getByRole("list", { name: "Services" });
  await expect(services.getByRole("listitem").filter({ hasText: LEDGER })).toContainText(
    "From traces",
  );
  await expect(services.getByRole("listitem").filter({ hasText: PAYMENTS })).not.toContainText(
    "From traces",
  );
  for (const r of await rows(page)) {
    if (r.service === LEDGER) expect(r.text).toContain("From traces");
    else expect(r.text).not.toContain("From traces");
  }
  await expect(
    page.getByText(
      "Derived from sampled traces; counts are lower than the real request count when traces are sampled.",
    ),
  ).toBeVisible();
  const body = await api<ServicesBody>(
    ctx,
    "GET",
    `/api/tenants/${tenant.id}/api-metrics/services?range=1h`,
  );
  expect(body.body.items.map((s) => [s.service, s.source])).toEqual([
    [LEDGER, "span"],
    [PAYMENTS, "metric"],
  ]);
});

test("the routes table sorts by a column and reverses", async () => {
  // default: requests, most first
  let r = await rows(page);
  for (let i = 1; i < r.length; i++)
    expect(r[i - 1]?.requests ?? 0).toBeGreaterThanOrEqual(r[i]?.requests ?? 0);

  const head = (name: string) =>
    page.getByRole("region", { name: "Routes" }).getByRole("columnheader", { name });
  await head("Route").getByRole("button").click();
  await page.waitForURL(/sort=route/);
  await expect(head("Route")).toHaveAttribute("aria-sort", "ascending");
  const byName = (x: { service: string; method: string; route: string }) =>
    `${x.service}\u0000${x.method}\u0000${x.route}`;
  await expect
    .poll(async () => (await rows(page)).map(byName))
    .toEqual((await rows(page)).map(byName).sort());
  r = await rows(page);
  expect(r.map(byName)).toEqual([...r.map(byName)].sort());

  await head("Route").getByRole("button").click();
  await expect(head("Route")).toHaveAttribute("aria-sort", "descending");
  await expect
    .poll(async () => (await rows(page)).map(byName))
    .toEqual([...r.map(byName)].reverse());

  await head("Requests").getByRole("button").click();
  await expect(head("Requests")).toHaveAttribute("aria-sort", "descending");
  await head("Requests").getByRole("button").click();
  await expect(head("Requests")).toHaveAttribute("aria-sort", "ascending");
  await expect
    .poll(async () => {
      const x = await rows(page);
      return x.every((row, i) => i === 0 || (x[i - 1]?.requests ?? 0) <= row.requests);
    })
    .toBe(true);
  await page.screenshot({ path: join(shots, "sorted.png"), fullPage: true });
});

test("filters scope every number to a service, a method and a route", async () => {
  // the services are chips, the methods a switch, the route a field
  const chip = page.locator(`[data-service-chip="${PAYMENTS}"]`);
  await chip.click();
  await page.waitForURL(new RegExp(`service=${PAYMENTS}`));
  await expect(chip).toHaveAttribute("aria-pressed", "true");
  await expect(requestsValue(page, sentFor(PAYMENTS))).toBeVisible();
  await expect.poll(async () => (await rows(page)).every((x) => x.service === PAYMENTS)).toBe(true);

  await page
    .getByRole("radiogroup", { name: "Method" })
    .getByRole("radio", { name: "POST", exact: true })
    .click();
  await page.waitForURL(/method=POST/);
  await expect.poll(async () => (await rows(page)).map((x) => x.method)).toEqual(["POST", "POST"]);

  // narrow to one route from its row menu
  await page.getByRole("button", { name: `Actions for POST /accounts of ${PAYMENTS}` }).click();
  await page.getByRole("menuitem", { name: "Filter to this route" }).click();
  await page.waitForURL(/route=%2Faccounts(&|$)/);
  const posted =
    (sent.get(key(PAYMENTS, "POST", "/accounts", 201)) ?? 0) +
    (sent.get(key(PAYMENTS, "POST", "/accounts", 400)) ?? 0);
  await expect(requestsValue(page, posted)).toBeVisible();
  await expect(page.getByRole("searchbox", { name: "Route" })).toHaveValue("/accounts");
  await expect.poll(async () => (await rows(page)).length).toBe(1);
  // the 400s of this route are its 4xx rate
  const bad = sent.get(key(PAYMENTS, "POST", "/accounts", 400)) ?? 0;
  await expect(page.locator('[data-tile="4xx rate"]')).toContainText(
    `${bad.toLocaleString("en")} client errors`,
  );
  await page.screenshot({ path: join(shots, "filtered.png"), fullPage: true });

  await page.getByRole("button", { name: "Clear filters" }).click();
  await expect(page.getByRole("button", { name: "Clear filters" })).toHaveCount(0);
  await expect(chip).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByRole("searchbox", { name: "Route" })).toHaveValue("");
  await expect(requestsValue(page, sentFor(PAYMENTS) + sentFor(LEDGER))).toBeVisible();
});

test("ranges switch between minute, hour and day data", async () => {
  // hourly rows come from the rollup, which runs every 5 minutes
  test.setTimeout(600_000);
  await waitExact(ctx, tenant.id, [PAYMENTS, LEDGER], "24h", 420_000);
  await pickRange(page, "24 hours");
  await page.waitForURL(/range=24h/);
  await expect(page.getByRole("heading", { name: "Requests per hour", exact: true })).toBeVisible();
  await expect(requestsValue(page, sentFor(PAYMENTS) + sentFor(LEDGER))).toBeVisible();

  // daily rows of the same rollup
  await waitExact(ctx, tenant.id, [PAYMENTS, LEDGER], "90d", 420_000);
  await pickRange(page, "90 days");
  await page.waitForURL(/range=90d/);
  // the range is answered per day; the chart narrows to the hours of the
  // last day when every request of the range fell on it
  await expect(page.locator("main").getByText("per day", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: /^Requests per (day|hour)$/ })).toBeVisible();
  await expect(requestsValue(page, sentFor(PAYMENTS) + sentFor(LEDGER))).toBeVisible();

  // a custom range of the last two hours is minute data again
  await pickRange(page, "Custom");
  const now = Date.now();
  const local = (ms: number) => new Date(ms).toISOString().slice(0, 16);
  const form = page.getByRole("form", { name: "Custom range" });
  await form.getByLabel("From (UTC)").fill(local(now - 2 * 3600_000));
  await form.getByLabel("To (UTC)").fill(local(now + 5 * 60_000));
  await form.getByRole("button", { name: "Apply range" }).click();
  await page.waitForURL(/range=custom/);
  await expect(
    page.getByRole("heading", { name: "Requests per minute", exact: true }),
  ).toBeVisible();
  await expect(requestsValue(page, sentFor(PAYMENTS) + sentFor(LEDGER))).toBeVisible();
  await page.screenshot({ path: join(shots, "custom-range.png"), fullPage: true });
});

test("the page works at phone width", async () => {
  await page.setViewportSize({ width: 375, height: 800 });
  await pickRange(page, "1 hour");
  await page.waitForURL(/range=1h/);
  await expect(page.getByRole("list", { name: "Services" })).toBeVisible();
  // nothing but the routes table scrolls sideways; on failure the message
  // names the elements that stick out
  await expect
    .poll(() =>
      page.evaluate(() => {
        const w = window.innerWidth;
        if (document.documentElement.scrollWidth <= w) return [];
        return [...document.querySelectorAll("body *")]
          .filter((e) => e.getBoundingClientRect().right > w + 1)
          .filter(
            (e) =>
              !e.closest(
                "[aria-label='Routes'], nav[aria-label='Project'], [role='radiogroup'][aria-label='Range']",
              ),
          )
          .slice(0, 5)
          .map((e) => `${e.tagName.toLowerCase()}.${e.getAttribute("class") ?? ""}`);
      }),
    )
    .toEqual([]);
  await page.screenshot({ path: join(shots, "phone.png"), fullPage: true });
});

test("the page ran without a csp violation or script error", () => {
  expect(problems).toEqual([]);
});

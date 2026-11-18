// operator global views and api keys, end to end. four projects get real browser traffic
// through the tracker, the gateway, the replayer and umami, and real api
// traffic through the example app, the collector and the metricswriter; the
// jobs daemon aggregates both. an operator then filters the global web view
// to verified projects, sorts by visitors, selects three projects and sees
// them side by side for web and for api, creates an api key in the console,
// reads /api/metrics/v1/tenants with it from a client without a session,
// and revokes it, after which the same request is refused. a non-operator
// gets 403 on every route involved.
//
// two stack fixtures stand in for what an offline stack cannot do itself
// (stack_test.go): POST /sites/verify records a domain verification, because
// the real check refuses the private addresses every host here resolves to,
// and POST /jobs/restart makes the jobs daemon run its hourly aggregate-web
// task now. every number checked below comes from the requests this spec made.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { type Browser, type BrowserContext, devices, type Page } from "@playwright/test";
import { APP, APPS_CTL, INGEST, OUT } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  enrollApi,
  type GitHubUser,
  newUser,
  operatorStepUp,
  pasteSnippet,
  type Site,
  signIn,
  type Tenant,
  uniqueHost,
  waitProvisioned,
  waitRegistered,
  webGet,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const OPERATOR: GitHubUser = { id: 1, login: "sa-operator" };
const shots = join(OUT, "operator-global");
// project names carry a run suffix, so the search filter isolates this run
const run = `g${Math.random().toString(36).slice(2, 7)}`;

interface Project {
  label: string;
  tenant: Tenant;
  site: Site;
  verified: boolean;
  // distinct browsers (user agents), each one visitor of the day
  visitors: number;
  // page views per visitor
  pages: number;
  // api requests the example app answers
  requests: number;
}

// a is first by visitors, b by page views; d has the most visitors of all
// but no verified site, so the verified filter is what keeps it out
const PLAN = [
  { label: "A", verified: true, visitors: 3, pages: 1, requests: 4 },
  { label: "B", verified: true, visitors: 2, pages: 3, requests: 8 },
  { label: "C", verified: true, visitors: 1, pages: 1, requests: 2 },
  { label: "D", verified: false, visitors: 4, pages: 1, requests: 1 },
];

let op: { ctx: BrowserContext; page: Page };
let owner: { ctx: BrowserContext; page: Page };
const projects: Project[] = [];
const problems: string[] = [];

function project(label: string): Project {
  const p = projects.find((x) => x.label === label);
  if (!p) throw new Error(`no project ${label}`);
  return p;
}

async function ctl(path: string, body?: unknown): Promise<void> {
  const r = await fetch(`${APPS_CTL}${path}`, {
    method: "POST",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  expect(r.status, await r.text()).toBe(204);
}

// a chromium user agent with its own major version, so each browser below
// is a different visitor (the visitor token hashes the user agent)
function userAgent(i: number): string {
  const base = devices["Desktop Chrome"].userAgent;
  return base.replace(/Chrome\/(\d+)/, (_m, v: string) => `Chrome/${Number(v) - i}`);
}

async function visit(browser: Browser, origin: string, i: number, pages: number) {
  const ctx = await browser.newContext({
    userAgent: userAgent(i),
    proxy: { server: test.info().project.use.proxy?.server ?? "" },
    ignoreHTTPSErrors: true,
  });
  try {
    const page = await ctx.newPage();
    const accepted: number[] = [];
    page.on("response", (r) => {
      if (r.request().method() !== "POST" || r.url() !== `${INGEST}/v1/e`) return;
      const body = JSON.parse(r.request().postData() ?? "{}") as { t?: string };
      if (body.t === "pv" && r.status() === 202) accepted.push(1);
    });
    for (let n = 0; n < pages; n++) {
      await page.goto(`${origin}/${n % 2 === 1 ? "about.html" : ""}`);
      await expect.poll(() => accepted.length, { timeout: 30_000 }).toBe(n + 1);
    }
  } finally {
    await ctx.close();
  }
}

async function startApp(service: string, token: string): Promise<string> {
  const r = await fetch(`${APPS_CTL}/apps`, {
    method: "POST",
    body: JSON.stringify({ service, token, signals: "metrics,traces" }),
  });
  const text = await r.text();
  expect(r.status, text).toBe(200);
  return (JSON.parse(text) as { url: string }).url;
}

interface WebRow {
  tenant: { id: string };
  visitors: number;
  pageviews: number;
}
interface ApiRow {
  tenant: { id: string };
  requests: number;
}
interface ListBody<T> {
  items: T[];
}

async function webNumbers(): Promise<Record<string, [number, number]>> {
  const r = await api<ListBody<WebRow>>(
    op.ctx,
    "GET",
    `/api/operator/web/projects?q=${run}&limit=50`,
  );
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  return Object.fromEntries(r.body.items.map((i) => [i.tenant.id, [i.visitors, i.pageviews]]));
}

async function apiNumbers(): Promise<Record<string, number>> {
  const r = await api<ListBody<ApiRow>>(
    op.ctx,
    "GET",
    `/api/operator/api/projects?q=${run}&limit=50`,
  );
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  return Object.fromEntries(r.body.items.map((i) => [i.tenant.id, i.requests]));
}

test.beforeAll(async ({ browser }) => {
  test.setTimeout(1_200_000);
  mkdirSync(shots, { recursive: true });

  const octx = await browser.newContext();
  op = { ctx: octx, page: await octx.newPage() };
  op.page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  op.page.on("console", (m) => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
  await signIn(op.page, OPERATOR);
  await operatorStepUp(op.page, OPERATOR);
  const me = await api<{ user: { is_operator: boolean } }>(op.ctx, "GET", "/api/me");
  expect(me.body.user.is_operator, "github id 1 must be an operator").toBe(true);

  const tctx = await browser.newContext();
  owner = { ctx: tctx, page: await tctx.newPage() };
  await signIn(owner.page, newUser("globalown"));

  for (const p of PLAN) {
    const tenant = await enrollApi(owner.ctx, `Global ${run} ${p.label}`);
    expect(tenant.status, "the stack enrolls in open mode").toBe("active");
    const site = await addSiteApi(
      owner.ctx,
      tenant.id,
      uniqueHost(`global${p.label.toLowerCase()}`),
    );
    projects.push({ ...p, tenant, site });
  }

  // web traffic: the snippet from the console's snippet route, pasted into
  // each site, and one browser per visitor
  for (const p of projects) {
    await waitProvisioned(owner.ctx, p.tenant.id);
    const origin = `https://${p.site.domain}`;
    await waitRegistered(owner.ctx, p.site.id, origin);
    const snip = await api<{ tag: string }>(owner.ctx, "GET", `/api/sites/${p.site.id}/snippet`);
    expect(snip.status).toBe(200);
    await pasteSnippet(p.site.domain, snip.body.tag);
    for (let i = 0; i < p.visitors; i++) await visit(browser, origin, i, p.pages);
    if (p.verified) await ctl("/sites/verify", { site_id: p.site.id });
  }

  // api traffic: one example app per project with its own otlp token
  for (const p of projects) {
    const tok = await api<{ token: string }>(
      owner.ctx,
      "POST",
      `/api/tenants/${p.tenant.id}/otlp-tokens`,
      {
        name: "global e2e",
      },
    );
    expect(tok.status, JSON.stringify(tok.body)).toBe(201);
    const base = await startApp(`global-${run}-${p.label.toLowerCase()}`, tok.body.token);
    for (let i = 0; i < p.requests; i++) {
      const res = await fetch(`${base}/accounts`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ name: `a${i}` }),
      });
      expect(res.status).toBe(201);
    }
  }

  // the replayer delivers within seconds; the aggregation reads umami when
  // the daemon starts, so it is restarted until the day's rows hold every
  // visit (a restart before the replayer finished reads too little)
  const wantWeb = Object.fromEntries(
    projects.map((p) => [p.tenant.id, [p.visitors, p.visitors * p.pages]]),
  );
  const same = (x: Record<string, [number, number]>) =>
    Object.keys(x).length === projects.length &&
    projects.every((p) => JSON.stringify(x[p.tenant.id]) === JSON.stringify(wantWeb[p.tenant.id]));
  let got: Record<string, [number, number]> = {};
  for (let attempt = 0; attempt < 6 && !same(got); attempt++) {
    await ctl("/jobs/restart");
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      got = await webNumbers();
      if (same(got)) break;
      await new Promise((r) => setTimeout(r, 3_000));
    }
  }
  expect(got, "operator web numbers equal the browser traffic sent").toEqual(wantWeb);
  // each project's own web dashboard reads the same aggregates for the same
  // 30 utc days, so the owner sees exactly the operators' numbers
  for (const p of projects) {
    const s = await webGet<{ current: { totals: { visitors: number; pageviews: number } } }>(
      owner.ctx,
      p.tenant.id,
      "summary",
    );
    expect(s.status, JSON.stringify(s.body)).toBe(200);
    expect([s.body.current.totals.visitors, s.body.current.totals.pageviews], p.label).toEqual(
      got[p.tenant.id],
    );
  }

  // daily api rows follow with the rollup every 5 minutes
  const wantApi = Object.fromEntries(projects.map((p) => [p.tenant.id, p.requests]));
  await expect
    .poll(apiNumbers, { timeout: 480_000, intervals: [2_000, 5_000, 10_000] })
    .toEqual(wantApi);
});

test.afterAll(async () => {
  await op?.ctx.close();
  await owner?.ctx.close();
});

test("a non-operator gets 403 on the global views and the key routes", async () => {
  const id = project("A").tenant.id;
  const routes: [string, string, unknown][] = [
    ["GET", "/api/operator/web/projects", undefined],
    ["GET", `/api/operator/web/compare?tenants=${id}`, undefined],
    ["GET", "/api/operator/api/projects", undefined],
    ["GET", `/api/operator/api/compare?tenants=${id}`, undefined],
    ["GET", "/api/operator/api-keys", undefined],
    ["POST", "/api/operator/api-keys", { name: "x", scope: "metrics:read" }],
    ["DELETE", `/api/operator/api-keys/${id}`, undefined],
  ];
  const failures: string[] = [];
  for (const [method, path, body] of routes) {
    const r = await api<{ error?: { code?: string } }>(owner.ctx, method, path, body);
    if (r.status !== 403 || r.body?.error?.code !== "operator_required")
      failures.push(`${method} ${path}: ${r.status} ${r.body?.error?.code}`);
  }
  expect(failures, "routes that did not answer 403 operator_required").toEqual([]);
  for (const path of [
    "/operator/projects?view=web",
    "/operator/projects?view=api",
    "/operator/keys",
  ]) {
    await owner.page.goto(`${APP}${path}`);
    await expect(owner.page.getByRole("heading", { name: "Operators only" })).toBeVisible();
  }
});

async function rowIds(page: Page): Promise<string[]> {
  return page
    .locator("tbody tr[data-tenant]")
    .evaluateAll((trs) => trs.map((tr) => tr.getAttribute("data-tenant") ?? ""));
}

test("the operator filters to verified projects, sorts by visitors and compares three", async () => {
  const page = op.page;
  const [a, b, c, d] = ["A", "B", "C", "D"].map(project) as [Project, Project, Project, Project];
  await page.goto(`${APP}/operator`);
  // the sidebar opens Projects, its view control picks the web view
  await page
    .getByRole("navigation", { name: "Operator" })
    .getByRole("link", { name: "Projects" })
    .click();
  await page.waitForURL(`${APP}/operator/projects`);
  await page.getByRole("radiogroup", { name: "View" }).getByRole("radio", { name: "Web" }).click();
  await page.waitForURL(`${APP}/operator/projects?view=web`);
  await expect(page.getByRole("heading", { name: "Global web" })).toBeVisible();
  await expect(page.getByText("IP Geolocation by DB-IP")).toBeVisible();
  await expect(page.getByText("Visitors are unique per UTC day and site")).toBeVisible();

  await page.getByLabel("Search", { exact: true }).fill(run);
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.waitForURL(new RegExp(`q=${run}`));
  // default sort is page views: b (6), d (4), a (3), c (1)
  await expect.poll(() => rowIds(page)).toEqual([b, d, a, c].map((p) => p.tenant.id));

  await page.getByLabel("Domain verified").selectOption("true");
  await page.waitForURL(/verified=true/);
  await expect.poll(() => rowIds(page)).toEqual([b, a, c].map((p) => p.tenant.id));
  await expect(page.getByText("3 projects match")).toBeVisible();

  await page.getByRole("button", { name: "Visitors", exact: true }).click();
  await page.waitForURL(/sort=visitors/);
  await expect.poll(() => rowIds(page)).toEqual([a, b, c].map((p) => p.tenant.id));
  const visitors = await page
    .locator("tbody tr[data-tenant] [data-col=visitors]")
    .allTextContents();
  expect(visitors).toEqual(["3", "2", "1"]);
  await expect(page.getByRole("columnheader", { name: "Visitors", exact: true })).toHaveAttribute(
    "aria-sort",
    "descending",
  );
  await page.screenshot({ path: join(shots, "global-web.png"), fullPage: true });

  for (const p of [a, b, c]) await page.getByLabel(`Compare ${p.tenant.name}`).check();
  await page
    .getByRole("region", { name: "Comparison" })
    .getByRole("link", { name: "Compare" })
    .click();
  await page.waitForURL(/\/operator\/compare\?tenants=/);
  const compared = page.getByRole("list", { name: "Compared projects" });
  await expect(compared.getByRole("listitem")).toHaveCount(3);
  await expect(compared).toContainText(a.tenant.name);

  // one row per compared project in the order of the comparison
  const webTotals = page.getByRole("region", { name: "Web totals" });
  await expect(webTotals.locator("td[data-metric='Visitors']")).toHaveText(["3", "2", "1"]);
  await expect(webTotals.locator("td[data-metric='Page views']")).toHaveText(["3", "6", "1"]);
  const apiTotals = page.getByRole("region", { name: "API totals" });
  await expect(apiTotals.locator("td[data-metric='Requests']")).toHaveText(["4", "8", "2"]);
  for (const title of ["Page views", "Visitors", "Requests", "p95 latency"])
    await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Devices" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Countries" })).toBeVisible();
  await expect(page.getByText("IP Geolocation by DB-IP")).toBeVisible();
  await page.screenshot({ path: join(shots, "compare.png"), fullPage: true });

  // the global api view with the same filter, sorted by requests ascending:
  // the lowest usage project first
  await page.goto(
    `${APP}/operator/projects?view=api&q=${run}&verified=true&sort=requests&order=asc`,
  );
  await expect.poll(() => rowIds(page)).toEqual([c, a, b].map((p) => p.tenant.id));
  const requests = await page
    .locator("tbody tr[data-tenant] [data-col=requests]")
    .allTextContents();
  expect(requests).toEqual(["2", "4", "8"]);
  await expect(page.locator(`tr[data-tenant='${a.tenant.id}']`)).toContainText("Metrics");
});

test("the pages work at phone width", async () => {
  const page = op.page;
  await page.setViewportSize({ width: 375, height: 800 });
  const ids = ["A", "B", "C"].map((l) => project(l).tenant.id).join(",");
  for (const path of [
    `/operator/projects?view=web&q=${run}`,
    `/operator/projects?view=api&q=${run}`,
    `/operator/compare?tenants=${ids}`,
    "/operator/keys",
  ]) {
    await page.goto(`${APP}${path}`);
    await expect(page.getByRole("heading", { level: 2 }).first()).toBeVisible();
    await page.waitForLoadState("networkidle");
    // only the scrollable tables and the operator tabs may be wider
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
                  "[aria-label='Global web table'], [aria-label='Global API table'], [aria-label='Web totals'], [aria-label='API totals'], [role='radiogroup'][aria-label='View']",
                ),
            )
            .slice(0, 5)
            .map((e) => `${e.tagName.toLowerCase()}.${e.getAttribute("class") ?? ""}`);
        }),
      )
      .toEqual([]);
    await page.screenshot({
      path: join(shots, `phone-${path.split("?")[0]?.split("/").pop()}.png`),
      fullPage: true,
    });
  }
  await page.setViewportSize({ width: 1280, height: 800 });
});

test("an api key is created, read with from a client without a session, and revoked", async ({
  browser,
}) => {
  const page = op.page;
  await page.goto(`${APP}/operator/keys`);
  const form = page.getByRole("form", { name: "Create an API key" });
  const name = `e2e ${run}`;
  await form.getByLabel("Name").fill(name);
  await form.getByLabel("Expires after (days)").fill("7");
  await form.getByRole("button", { name: "Create key" }).click();
  // the one-time secret is masked until it is revealed
  const shown = page.getByRole("region", { name: "New secret" });
  await expect(shown).toBeVisible();
  await shown.getByRole("button", { name: "Show the secret" }).click();
  // the masked text crossfades out before the key fades in
  await expect(shown.locator("code").first()).toHaveText(/^sa_op_[0-9A-Za-z]{40}$/);
  const key = (await shown.locator("code").first().textContent())?.trim() ?? "";
  expect(key).toMatch(/^sa_op_[0-9A-Za-z]{40}$/);
  await expect(page.getByRole("button", { name: "Copy key" })).toBeVisible();

  // a machine client: a fresh context with no cookies, only the key
  const machine = await browser.newContext({
    proxy: { server: test.info().project.use.proxy?.server ?? "" },
    ignoreHTTPSErrors: true,
  });
  try {
    const get = (headers: Record<string, string>) =>
      machine.request.get(`${APP}/api/metrics/v1/tenants`, { headers, maxRedirects: 0 });
    const ok = await get({ authorization: `Bearer ${key}` });
    expect(ok.status(), await ok.text()).toBe(200);
    const listed = ((await ok.json()) as { items: { id: string }[] }).items.map((t) => t.id);
    for (const p of projects) expect(listed).toContain(p.tenant.id);

    // the operator's session cookie opens nothing there
    const cookieOnly = await op.ctx.request.get(`${APP}/api/metrics/v1/tenants`, {
      maxRedirects: 0,
    });
    expect(cookieOnly.status()).toBe(401);

    // the key is shown once: after dismissing, only its prefix remains
    await expect(shown.getByRole("button", { name: "Done" })).toBeDisabled();
    await shown.getByRole("checkbox").check();
    await shown.getByRole("button", { name: "Done" }).click();
    await expect(shown).toHaveCount(0);
    const row = page.getByRole("listitem", { name: `Key ${name}` });
    await expect(row).toContainText(key.slice(0, 10));
    await expect(page.getByText(key)).toHaveCount(0);
    await row.getByRole("button", { name: "Usage" }).click();
    await expect(row.locator("[data-usage=requests]")).toHaveText("1");

    await row.getByRole("button", { name: "Revoke" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("The next request with this key is refused");
    await dialog.getByRole("button", { name: "Revoke key" }).click();
    await expect(dialog).toBeHidden();
    await expect(row).toContainText("Revoked");

    const refused = await get({ authorization: `Bearer ${key}` });
    expect(refused.status()).toBe(401);
    expect(((await refused.json()) as { error: { code: string } }).error.code).toBe(
      "unauthenticated",
    );
  } finally {
    await machine.close();
  }
  await page.screenshot({ path: join(shots, "api-keys.png"), fullPage: true });
});

test("the pages ran without a csp violation or script error", () => {
  expect(problems).toEqual([]);
});

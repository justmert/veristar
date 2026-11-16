// the integrity grade, end to end. a project gets real browser traffic
// through the tracker, the gateway, the replayer and umami; the gateway
// classifies every event's network from its open asn data, the jobs daemon
// aggregates the day and grades it. the project's owner then reads the
// grade with its reasons on the project overview, and an operator reads
// the same grade in the global web table (sorting by it and opening its
// reasons), on the project's detail page and in the comparison. another
// project's member gets 404 on the project route and a non-operator 403 on
// the operator route.
//
// every address in the stack is private, so the open data places none of
// them: the events are counted in the unknown class and no network is
// counted. two visitors with 26 page views each give 52 accepted events, so
// the day is graded, and no rule lowers it.
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
  pasteSnippet,
  type Site,
  signIn,
  type Tenant,
  uniqueHost,
  waitProvisioned,
  waitRegistered,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const OPERATOR: GitHubUser = { id: 1, login: "sa-operator" };
const shots = join(OUT, "integrity");
const run = `i${Math.random().toString(36).slice(2, 7)}`;
const VISITORS = 2;
const PAGES = 26;

let op: { ctx: BrowserContext; page: Page };
let owner: { ctx: BrowserContext; page: Page };
let outsider: BrowserContext;
let tenant: Tenant;
let site: Site;
const problems: string[] = [];

interface Report {
  tenant_id: string;
  grade: string | null;
  graded_days: number;
  sites: {
    site_id: string;
    days: {
      day: string;
      grade: string | null;
      reasons: { code: string }[];
      evidence: { events: number; networks: number; net: Record<string, number> };
    }[];
  }[];
}

async function ctl(path: string): Promise<void> {
  const r = await fetch(`${APPS_CTL}${path}`, { method: "POST" });
  expect(r.status, await r.text()).toBe(204);
}

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

async function ownReport(): Promise<Report> {
  const r = await api<Report>(owner.ctx, "GET", `/api/tenants/${tenant.id}/integrity`);
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  return r.body;
}

test.beforeAll(async ({ browser }) => {
  test.setTimeout(900_000);
  mkdirSync(shots, { recursive: true });

  const octx = await browser.newContext();
  op = { ctx: octx, page: await octx.newPage() };
  op.page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  op.page.on("console", (m) => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
  await signIn(op.page, OPERATOR);

  const tctx = await browser.newContext();
  owner = { ctx: tctx, page: await tctx.newPage() };
  owner.page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  await signIn(owner.page, newUser("integrityown"));
  outsider = await browser.newContext();
  await signIn(await outsider.newPage(), newUser("integrityout"));

  tenant = await enrollApi(owner.ctx, `Integrity ${run}`);
  expect(tenant.status, "the stack enrolls in open mode").toBe("active");
  site = await addSiteApi(owner.ctx, tenant.id, uniqueHost("integrity"));
  await waitProvisioned(owner.ctx, tenant.id);
  const origin = `https://${site.domain}`;
  await waitRegistered(owner.ctx, site.id, origin);
  const snip = await api<{ tag: string }>(owner.ctx, "GET", `/api/sites/${site.id}/snippet`);
  expect(snip.status).toBe(200);
  await pasteSnippet(site.domain, snip.body.tag);
  for (let i = 0; i < VISITORS; i++) await visit(browser, origin, i, PAGES);

  // aggregate-web and integrity both run when the daemon starts; the grade
  // needs the day's aggregate, so the daemon is restarted until a graded day
  // with every event is there
  let report: Report | null = null;
  const graded = (r: Report | null) =>
    r?.sites[0]?.days.some((d) => d.grade !== null && d.evidence.events === VISITORS * PAGES) ??
    false;
  for (let attempt = 0; attempt < 8 && !graded(report); attempt++) {
    await ctl("/jobs/restart");
    const deadline = Date.now() + 45_000;
    while (Date.now() < deadline) {
      report = await ownReport();
      if (graded(report)) break;
      await new Promise((r) => setTimeout(r, 3_000));
    }
  }
  expect(graded(report), JSON.stringify(report)).toBe(true);
});

test.afterAll(async () => {
  await op?.ctx.close();
  await owner?.ctx.close();
  await outsider?.close();
});

test("the gateway classified every event and the job graded the day", async () => {
  const report = await ownReport();
  expect(report.tenant_id).toBe(tenant.id);
  expect(report.sites.map((s) => s.site_id)).toEqual([site.id]);
  const day = report.sites[0]?.days.find((d) => d.grade !== null);
  if (!day) throw new Error("no graded day");
  expect(day.grade).toBe("A");
  expect(day.reasons).toEqual([]);
  // private addresses belong to no autonomous system: all events unknown,
  // no network counted
  expect(day.evidence.net).toEqual({
    access: 0,
    organisation: 0,
    hosting: 0,
    anonymiser: 0,
    unknown: VISITORS * PAGES,
  });
  expect(day.evidence.networks).toBe(0);
  expect(report.grade).toBe("A");
});

test("the project reads its own grade on the overview, as operators see it", async () => {
  const page = owner.page;
  await page.goto(`${APP}/projects/${tenant.id}`);
  const panel = page.getByRole("region", { name: "Integrity" });
  await expect(panel).toBeVisible();
  await expect(panel.getByText("exactly the report SCF operators see")).toBeVisible();
  await expect(panel.locator("[data-grade]").first()).toHaveText("Grade A");
  await expect(panel.getByText("IP to ASN data by DB-IP")).toBeVisible();
  await panel.getByText(site.name).click();
  await expect(panel.getByText("Accepted events").first()).toBeVisible();
  await page.screenshot({ path: join(shots, "overview.png"), fullPage: true });
});

test("another project's member and a non-operator are refused", async () => {
  const other = await api<{ error?: { code?: string } }>(
    outsider,
    "GET",
    `/api/tenants/${tenant.id}/integrity`,
  );
  expect(other.status).toBe(404);
  const notOp = await api<{ error?: { code?: string } }>(
    owner.ctx,
    "GET",
    `/api/operator/web/integrity?tenant=${tenant.id}`,
  );
  expect(notOp.status).toBe(403);
  expect(notOp.body?.error?.code).toBe("operator_required");
});

test("the operator sorts the global web table by grade and opens the reasons", async () => {
  const page = op.page;
  await page.goto(`${APP}/operator/web?q=${run}`);
  const row = page.locator(`tbody tr[data-tenant="${tenant.id}"]`);
  await expect(row).toBeVisible();
  const cell = row.locator("[data-col=integrity]");
  await expect(cell).toHaveText("Grade A");

  await page.getByRole("button", { name: "Integrity", exact: true }).click();
  await page.waitForURL(/sort=integrity/);
  await expect(row).toBeVisible();

  await cell.click();
  const dialog = page.getByRole("dialog", { name: `Integrity of ${tenant.name}` });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("region", { name: "Integrity" })).toBeVisible();
  await expect(dialog.getByText(site.domain)).toBeVisible();
  await page.screenshot({ path: join(shots, "global-web-dialog.png") });
  await dialog.getByRole("button", { name: "Close" }).first().click();

  // the same report on the project's detail page and in the comparison
  await page.goto(`${APP}/operator/tenants/${tenant.id}`);
  const detail = page.getByRole("region", { name: "Integrity" });
  await expect(detail.locator("[data-grade]").first()).toHaveText("Grade A");
  await page.goto(`${APP}/operator/compare?tenants=${tenant.id}`);
  const cmp = page.getByRole("list", { name: "Integrity per project" });
  await expect(cmp.locator("[data-grade]").first()).toHaveText("Grade A");
  expect(problems, "console errors on the operator pages").toEqual([]);
});

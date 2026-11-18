// the estimate of visits not measured because of privacy signals, end to
// end. a project pastes the console's snippet, which pins tracker 1.3.1 as
// the recommended release. three browsers load the site; one of them sends
// Sec-GPC: 1 on every request. the gateway attributes each request for the
// tracker file to the site by the page's origin in the Referer header and
// counts it, and the gpc one again; the jobs daemon stores both counters.
// the project then reads the counts on its overview and what scf sees, and
// an operator reads them in the global web table's column and in its export.
// three loads are below the 100 the share needs, so every view states the
// counts and says the share cannot be estimated yet.
import { join } from "node:path";
import type { BrowserContext, Page } from "@playwright/test";
import { APP, OUT } from "../fixture/env.ts";
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
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const OPERATOR: GitHubUser = { id: 1, login: "sa-operator" };
const run = `c${Math.random().toString(36).slice(2, 7)}`;
const shots = join(OUT, "coverage");

let op: { ctx: BrowserContext; page: Page };
let owner: { ctx: BrowserContext; page: Page };
let tenant: Tenant;
let site: Site;
let tag = "";

interface Coverage {
  script_requests: number;
  gpc_requests: number;
  unmeasured_share: number | null;
}

async function ownCoverage(): Promise<Coverage> {
  const r = await api<{ coverage: Coverage }>(
    owner.ctx,
    "GET",
    `/api/tenants/${tenant.id}/integrity`,
  );
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  return r.body.coverage;
}

test.beforeAll(async ({ browser }) => {
  test.setTimeout(600_000);
  const octx = await browser.newContext();
  op = { ctx: octx, page: await octx.newPage() };
  await signIn(op.page, OPERATOR);
  // the operator views need a passkey step-up on top of the sign-in
  await operatorStepUp(op.page, OPERATOR);
  const tctx = await browser.newContext();
  owner = { ctx: tctx, page: await tctx.newPage() };
  await signIn(owner.page, newUser("coverageown"));

  tenant = await enrollApi(owner.ctx, `Coverage ${run}`);
  expect(tenant.status).toBe("active");
  site = await addSiteApi(owner.ctx, tenant.id, uniqueHost("coverage"));
  await waitProvisioned(owner.ctx, tenant.id);
  await waitRegistered(owner.ctx, site.id, `https://${site.domain}`);
  const snip = await api<{ tag: string; version: string }>(
    owner.ctx,
    "GET",
    `/api/sites/${site.id}/snippet`,
  );
  expect(snip.status).toBe(200);
  expect(snip.body.version, "the console recommends the newest release").toBe("1.3.1");
  tag = snip.body.tag;
  await pasteSnippet(site.domain, tag);

  const proxy = { server: test.info().project.use.proxy?.server ?? "" };
  for (const gpc of [false, false, true]) {
    const ctx = await browser.newContext({
      proxy,
      ignoreHTTPSErrors: true,
      extraHTTPHeaders: gpc ? { "Sec-GPC": "1" } : {},
    });
    try {
      const page = await ctx.newPage();
      const loaded = page.waitForResponse((r) => r.url().endsWith("/v/1.3.1/sdk.js"));
      await page.goto(`https://${site.domain}/`);
      expect((await loaded).status()).toBe(200);
    } finally {
      await ctx.close();
    }
  }
  // the gateway reports every 10 s and the jobs daemon drains continuously
  await expect
    .poll(async () => (await ownCoverage()).script_requests, { timeout: 120_000 })
    .toBe(3);
});

test.afterAll(async () => {
  await op?.ctx.close();
  await owner?.ctx.close();
});

test("the edge counted every load of the tracker file and the gpc one", async () => {
  expect(await ownCoverage()).toEqual({
    script_requests: 3,
    gpc_requests: 1,
    unmeasured_share: null,
  });
});

test("the snippet offers the title opt-in from 1.3.0", async () => {
  const page = owner.page;
  await page.goto(`${APP}/projects/${tenant.id}/sites/${site.id}`);
  // the newest release, which reads the title choice like every release from 1.3.0
  await expect(page.getByText("tracker 1.3.1").first()).toBeVisible();
  await page.getByLabel("Send the page title").check();
  await expect(page.locator("pre code").first()).toContainText('data-send-title="true"');
  expect(tag).not.toContain("data-send-title");
});

test("the project reads the estimate in what scf sees", async () => {
  const page = owner.page;
  await page.goto(`${APP}/projects/${tenant.id}/scf`);
  const scf = page.getByRole("region", { name: "Visits not measured" });
  await expect(scf.getByTestId("coverage")).toContainText("Of 3 loads of the tracker file");
});

test("the operator reads the column and the export", async () => {
  const page = op.page;
  await page.goto(`${APP}/operator/projects?view=web&q=${run}`);
  const row = page.locator(`tbody tr[data-tenant="${tenant.id}"]`);
  await expect(row).toBeVisible();
  // the column is one of the optional ones: the column menu shows it
  await page.getByRole("button", { name: "Columns" }).click();
  await page.getByRole("menuitemcheckbox", { name: "Not measured" }).click();
  await page.keyboard.press("Escape");
  const cell = row.locator("[data-col=unmeasured_share]");
  await expect(cell).toHaveText("–no estimate");
  await expect(row.getByTestId("coverage-detail")).toHaveText(
    "too few loads, 1 of 3 loads with GPC",
  );
  await page
    .getByRole("columnheader", { name: "Not measured", exact: true })
    .getByRole("button")
    .click();
  await page.waitForURL(/sort=unmeasured_share/);
  await page.screenshot({ path: join(shots, "global-web.png") });

  const csv = await op.ctx.request.get(`${APP}/api/operator/web/projects.csv?q=${run}`);
  expect(csv.status()).toBe(200);
  const [head, ...rows] = (await csv.text()).trim().split("\n");
  const cols = (head ?? "").split(",");
  const row0 = (rows.find((r) => r.startsWith(tenant.id)) ?? "").split(",");
  expect(row0[cols.indexOf("script_requests")]).toBe("3");
  expect(row0[cols.indexOf("gpc_requests")]).toBe("1");
  expect(row0[cols.indexOf("unmeasured_share")]).toBe("");
});

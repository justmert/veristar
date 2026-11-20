// the public demo, end to end. an owner enrolls two projects; the stack
// fixture POST /tenants/demo (stack_test.go) marks one of them as the demo,
// as a deployment would. a browser without any session then opens /demo,
// lands on the demo project, reads its project pages and the operator
// projects and compare views through /api/demo, and finds no control that
// writes. the other project never shows, /api/demo answers 404 for it, and
// every state change on /api/demo is refused with demo_read_only.
import type { BrowserContext, Page } from "@playwright/test";
import { APP, APPS_CTL } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  enrollApi,
  newUser,
  signIn,
  type Tenant,
  uniqueHost,
  waitProvisioned,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

test.describe.configure({ mode: "serial" });

const run = `d${Math.random().toString(36).slice(2, 7)}`;
const DEMO_NAME = `Demo ${run}`;
const PRIVATE_NAME = `Private ${run}`;

let demo: Tenant;
let hidden: Tenant;

// the names of controls that change something or export a file; none may
// show in the demo. picking projects for a comparison changes nothing and
// stays
const WRITES =
  /^(Add site|Add account or contract|Sign a report|Enroll a project|Propose a cohort|Edit$|Delete site|Delete rule|Revoke|Accept the terms|Actions$|New rule|Connect API|Audit log|Prove control|Invite)/;

async function noWriteControls(page: Page) {
  await expect(page.getByRole("button", { name: WRITES })).toHaveCount(0);
  await expect(page.getByRole("link", { name: WRITES })).toHaveCount(0);
  await expect(page.getByRole("link", { name: /CSV/ })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Account menu/ })).toHaveCount(0);
}

async function demoPage(page: Page, heading: string) {
  await expect(page.getByRole("heading", { name: heading, level: 1, exact: true })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await expect(page.locator("[data-demo-banner]")).toBeVisible();
  await expect(page.locator("body")).not.toContainText(PRIVATE_NAME);
  await noWriteControls(page);
}

test.beforeAll(async ({ browser }) => {
  test.setTimeout(600_000);
  const ctx = await browser.newContext();
  try {
    const page = await ctx.newPage();
    await signIn(page, newUser("demoown"));
    demo = await enrollApi(ctx, DEMO_NAME);
    hidden = await enrollApi(ctx, PRIVATE_NAME);
    for (const t of [demo, hidden]) {
      await addSiteApi(ctx, t.id, uniqueHost(t === demo ? "demo" : "private"));
      await waitProvisioned(ctx, t.id);
    }
  } finally {
    await ctx.close();
  }
  const r = await fetch(`${APPS_CTL}/tenants/demo`, {
    method: "POST",
    body: JSON.stringify({ tenant_id: demo.id }),
  });
  expect(r.status, await r.text()).toBe(204);
});

test("an anonymous tab browses the demo read-only", async ({ browser }) => {
  const ctx: BrowserContext = await browser.newContext();
  const page = await ctx.newPage();
  // every answer of /api the pages asked for; none may be refused
  const refused: string[] = [];
  page.on("response", (r) => {
    if (new URL(r.url()).pathname.startsWith("/api/") && r.status() >= 400)
      refused.push(`${r.status()} ${r.request().method()} ${r.url()}`);
  });
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  // /demo opens the oldest demo project; a retry of this file enrolls and
  // marks another, so the test then opens its own
  await page.goto(`${APP}/demo`);
  await page.waitForURL(/\/projects\/[0-9a-f-]{36}$/);
  if (!page.url().endsWith(demo.id)) await page.goto(`${APP}/projects/${demo.id}`);
  const banner = page.locator("[data-demo-banner]");
  await expect(banner).toContainText("Demo with sample data. Read-only.");
  await expect(banner.getByRole("link", { name: "Enroll your project" })).toHaveAttribute(
    "href",
    "/sign-in",
  );
  await demoPage(page, "Overview");

  // the project pages through the sidebar, as a visitor reads them
  const nav = page.locator("aside").getByRole("navigation", { name: "Project" });
  await expect(nav.getByRole("link")).toHaveText([
    "Overview",
    "Web",
    "API",
    "On-chain",
    "SCF review",
    "Sites",
  ]);
  for (const name of ["Web", "API", "On-chain", "SCF review", "Sites"]) {
    await nav.getByRole("link", { name, exact: true }).click();
    await demoPage(page, name);
  }

  // a reload keeps the tab in the demo
  await page.goto(`${APP}/projects/${demo.id}/web`);
  await demoPage(page, "Web");

  // the operator area opens on its project list, with no step-up
  await page.goto(`${APP}/operator`);
  await page.waitForURL(`${APP}/operator/projects`);
  await demoPage(page, "All projects");
  await expect(page.locator("body")).toContainText(DEMO_NAME);
  for (const view of ["web", "api"]) {
    await page.goto(`${APP}/operator/projects?view=${view}`);
    await demoPage(page, "All projects");
  }
  await page.goto(`${APP}/operator/compare?tenants=${demo.id}`);
  await demoPage(page, "Compare");
  await page.goto(`${APP}/operator/projects/${demo.id}`);
  await demoPage(page, DEMO_NAME);
  await page.goto(`${APP}/operator/rules`);
  await demoPage(page, "Rules");

  expect(refused, "no page of the demo asks a route the demo refuses").toEqual([]);
  expect(errors).toEqual([]);

  // a page the demo cannot draw is not found, by address too
  for (const path of [`/projects/${demo.id}/members`, "/operator/audit", "/account"]) {
    await page.goto(`${APP}${path}`);
    await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  }
  // and the other project is not there at all
  await page.goto(`${APP}/projects/${hidden.id}`);
  await expect(page.getByRole("heading", { name: "Project not found" })).toBeVisible();
  await expect(page.locator("body")).not.toContainText(PRIVATE_NAME);
  await page.goto(`${APP}/operator/projects/${hidden.id}`);
  await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  await expect(page.locator("body")).not.toContainText(PRIVATE_NAME);

  // the enroll link leaves the demo: sign-in and the console behind it read
  // the real api again
  await page.goto(`${APP}/projects/${demo.id}`);
  await banner.getByRole("link", { name: "Enroll your project" }).click();
  await page.waitForURL(`${APP}/sign-in`);
  expect(await page.evaluate(() => sessionStorage.getItem("sa-console.demo"))).toBeNull();
  await ctx.close();
});

test("the demo api serves the demo project only, and only reads", async ({ browser }) => {
  const ctx = await browser.newContext();
  const me = await api<{
    demo: boolean;
    user: { display_name: string; is_operator: boolean };
    tenants: { id: string; role: string }[];
  }>(ctx, "GET", "/api/demo/me");
  expect(me.status, JSON.stringify(me.body)).toBe(200);
  expect(me.body.demo).toBe(true);
  expect(me.body.user.display_name).toBe("Demo");
  expect(me.body.tenants.map((t) => t.id)).toContain(demo.id);
  expect(me.body.tenants.map((t) => t.id)).not.toContain(hidden.id);
  expect(new Set(me.body.tenants.map((t) => t.role))).toEqual(new Set(["viewer"]));

  const other = await api(ctx, "GET", `/api/demo/tenants/${hidden.id}`);
  expect(other.status).toBe(404);

  const readOnly = { error: expect.objectContaining({ code: "demo_read_only" }) };
  for (const [method, path, body] of [
    ["POST", `/api/demo/tenants/${demo.id}/sites`, { name: "x", domain: "x.example", origins: [] }],
    ["POST", "/api/demo/tenants", { name: "x" }],
    ["DELETE", `/api/demo/tenants/${demo.id}/members/${demo.id}`, undefined],
    ["GET", `/api/demo/tenants/${demo.id}/members`, undefined],
  ] as const) {
    const r = await api(ctx, method, path, body);
    expect(r.status, `${method} ${path}`).toBe(403);
    expect(r.body, `${method} ${path}`).toEqual(readOnly);
  }
  await ctx.close();
});

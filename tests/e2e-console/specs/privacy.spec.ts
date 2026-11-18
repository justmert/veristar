// erasure by session id and site reset, through the console's
// privacy page against the real stack: two visitors browse a tenant site
// through the gateway, the tenant owner finds one of them in the form's
// session list and erases it by its id, then resets the whole site, and the
// figures, read through the console's web dashboard as the owner, show
// exactly what is left after each step.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { type BrowserContext, devices, type Page } from "@playwright/test";
import { APP, INGEST, OUT } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  enrollApi,
  newUser,
  pasteSnippet,
  type Site,
  signIn,
  type Tenant,
  uniqueHost,
  waitProvisioned,
  waitRegistered,
  waitWebReady,
  webGet,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

const shots = join(OUT, "privacy");

test.describe.configure({ mode: "serial" });
test.use({ timezoneId: "UTC" });

let owner: BrowserContext;
let tenant: Tenant;
let site: Site;
let host = "";
// umami session ids of the two visitors
let erased = "";
let kept = "";

interface ErasureRow {
  id: string;
  kind: string;
  status: string;
  outcome: string | null;
  message: string | null;
  sessions_deleted: number;
  events_deleted: number;
  session_id: string | null;
  reaggregated_at: string | null;
}

test.beforeAll(async ({ browser }) => {
  owner = await browser.newContext({ timezoneId: "UTC" });
  host = uniqueHost("privacy");
  mkdirSync(shots, { recursive: true });
});
test.afterAll(async () => {
  await owner?.close();
});

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

// the site's sessions of today as the console's web dashboard lists them
// for the erasure of one session: each session's id and its page views
async function sessionViews(): Promise<Map<string, number>> {
  const r = await webGet<{ items: { id: string; views: number }[] }>(
    owner,
    tenant.id,
    `sessions?site=${site.id}&day=${today()}`,
  );
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  return new Map(r.body.items.map((s) => [s.id, s.views]));
}

async function sessions(): Promise<string[]> {
  return [...(await sessionViews()).keys()];
}

// today's page views of the site, per hour from the engine, added up
async function pageviews(): Promise<number> {
  const r = await webGet<{ points: { pageviews: number }[] }>(
    owner,
    tenant.id,
    `hourly?site=${site.id}&day=${today()}`,
  );
  expect(r.status).toBe(200);
  return r.body.points.reduce((a, p) => a + p.pageviews, 0);
}

// one visit from a fresh browser context, waiting for the gateway's 202
async function visit(ctx: BrowserContext, path: string): Promise<void> {
  const p = await ctx.newPage();
  const accepted = p.waitForResponse(
    (r) => r.request().method() === "POST" && r.url() === `${INGEST}/v1/e` && r.status() === 202,
  );
  await p.goto(`https://${host}${path}`);
  await accepted;
  await p.close();
}

async function latest(): Promise<ErasureRow> {
  const r = await api<{ items: ErasureRow[] }>(owner, "GET", `/api/tenants/${tenant.id}/erasure`);
  expect(r.status).toBe(200);
  const row = r.body.items[0];
  if (!row) throw new Error("no erasure request recorded");
  return row;
}

async function privacyPage(): Promise<Page> {
  const page = await owner.newPage();
  await page.goto(`${APP}/projects/${tenant.id}/privacy`);
  // the sidebar marks the privacy page as current
  await expect(page.getByRole("heading", { name: "Privacy", level: 1 })).toBeVisible();
  await expect(
    page.getByRole("navigation", { name: "Project" }).getByRole("link", { name: "Privacy" }),
  ).toHaveAttribute("aria-current", "page");
  return page;
}

test("two visitors reach umami through the gateway", async ({ browser }) => {
  const page = await owner.newPage();
  await signIn(page, newUser("privacy"));
  tenant = await enrollApi(owner, `Privacy project ${host}`);
  site = await addSiteApi(owner, tenant.id, host);
  const snip = await api<{ tag: string }>(owner, "GET", `/api/sites/${site.id}/snippet`);
  expect(snip.status).toBe(200);
  await pasteSnippet(host, snip.body.tag);
  await waitProvisioned(owner, tenant.id);
  await waitRegistered(owner, site.id, `https://${host}`);

  const proxy = { server: test.info().project.use.proxy?.server ?? "" };
  const desktop = await browser.newContext({ ignoreHTTPSErrors: true, proxy, timezoneId: "UTC" });
  const phone = await browser.newContext({
    ...devices["Pixel 7"],
    ignoreHTTPSErrors: true,
    proxy,
    timezoneId: "UTC",
  });
  try {
    await visit(desktop, "/");
    await visit(desktop, "/about.html");
    await visit(phone, "/");
  } finally {
    await desktop.close();
    await phone.close();
  }

  await waitWebReady(owner, tenant.id, site.id);
  await expect.poll(pageviews, { timeout: 120_000, intervals: [1000, 2000, 5000] }).toBe(3);
  await expect.poll(async () => (await sessions()).length, { timeout: 60_000 }).toBe(2);

  // the desktop visitor's session holds two pageviews, the phone's one
  const views = await sessionViews();
  for (const [id, n] of views) {
    if (n === 2) erased = id;
    else if (n === 1) kept = id;
  }
  const ids = [...views.entries()].map(([id, n]) => `${id}=${n}`);
  expect(erased, `sessions ${ids.join(", ")}`).not.toBe("");
  expect(kept).not.toBe("");
  await page.close();
});

test("the privacy page erases one session by its id and leaves the other", async () => {
  const page = await privacyPage();
  await expect(page.getByText("No erasure was requested yet.")).toBeVisible();
  await page.screenshot({ path: join(shots, "01-privacy.png"), fullPage: true });

  const form = page.getByRole("form", { name: "Request erasure" });
  // the kind is a radio card; a person clicks the card's title
  await form.getByText("One session", { exact: true }).click();
  await expect(form.getByRole("radio", { name: "One session" })).toBeChecked();
  // the owner finds the desktop visitor's session, the one with two page
  // views, in the form's list of today's sessions
  await form
    .getByRole("list", { name: "Sessions" })
    .getByRole("button")
    .filter({ hasText: "2 page views" })
    .click();
  await expect(form.getByLabel("Session id")).toHaveValue(erased);
  const sent = page.waitForResponse(
    (r) => r.request().method() === "POST" && r.url() === `${APP}/api/tenants/${tenant.id}/erasure`,
  );
  await form.getByRole("button", { name: "Request erasure" }).click();
  // nothing is erased before the confirmation
  const confirm = page.getByRole("dialog", { name: `Erase this session on ${host}?` });
  await confirm.getByRole("button", { name: "Erase data" }).click();
  expect((await sent).status()).toBe(202);
  await expect(page.getByText("The erasure request is sent.")).toBeVisible();
  await expect(page.getByText(`One session on ${host}: erased`)).toBeVisible({ timeout: 120_000 });
  await page.screenshot({ path: join(shots, "02-session-erased.png"), fullPage: true });

  const row = await latest();
  expect(row).toMatchObject({
    kind: "session",
    status: "done",
    outcome: "erased",
    session_id: erased,
    sessions_deleted: 1,
    events_deleted: 2,
  });
  // the history on the page shows the finished request with its counts
  const history = page.getByRole("table", { name: "Request history" });
  await expect(history.getByText(`One session: ${host}`)).toBeVisible();
  await expect(history.getByText(/Deleted 1 session, 2 events and 0 undeliverable/)).toBeVisible();

  // the erased session and its page views are gone, the other stays
  const left = await sessions();
  expect(left).not.toContain(erased);
  expect(left).toEqual([kept]);
  expect(await pageviews()).toBe(1);
  await page.close();
});

test("the privacy page resets the whole site after the domain is typed and confirmed", async () => {
  const page = await privacyPage();
  const form = page.getByRole("form", { name: "Request erasure" });
  // the kind is a radio card; a person clicks the card's title
  await form.getByText("Whole site", { exact: true }).click();
  await expect(form.getByRole("radio", { name: "Whole site" })).toBeChecked();
  await form.getByRole("button", { name: "Reset site data" }).click();
  const dialog = page.getByRole("dialog", { name: `Reset all web data of ${host}?` });
  await expect(dialog).toBeVisible();
  // the domain is typed in the dialog, exactly
  const reset = dialog.getByRole("button", { name: "Reset site data" });
  await dialog.getByLabel(`Type ${host} to confirm`).fill(`x${host}`);
  await expect(reset).toBeDisabled();
  await dialog.getByLabel(`Type ${host} to confirm`).fill(host);
  await expect(reset).toBeEnabled();
  await page.screenshot({ path: join(shots, "03-reset-confirm.png"), fullPage: true });
  const sent = page.waitForResponse(
    (r) => r.request().method() === "POST" && r.url() === `${APP}/api/tenants/${tenant.id}/erasure`,
  );
  await dialog.getByRole("button", { name: "Reset site data" }).click();
  expect((await sent).status()).toBe(202);
  await expect(page.getByText(`Whole site on ${host}: erased`)).toBeVisible({ timeout: 120_000 });
  await page.screenshot({ path: join(shots, "04-reset-done.png"), fullPage: true });

  const row = await latest();
  expect(row).toMatchObject({ kind: "site_reset", status: "done", outcome: "erased" });
  expect(row.sessions_deleted).toBe(1);

  expect(await sessions()).toEqual([]);
  expect(await pageviews()).toBe(0);

  // both requests are in the history, newest first, and the follow-up job
  // recomputes the aggregates of each
  const all = await api<{ items: ErasureRow[] }>(owner, "GET", `/api/tenants/${tenant.id}/erasure`);
  expect(all.body.items.map((r) => r.kind)).toEqual(["site_reset", "session"]);
  await expect
    .poll(
      async () =>
        (
          await api<{ items: ErasureRow[] }>(owner, "GET", `/api/tenants/${tenant.id}/erasure`)
        ).body.items.every((r) => r.reaggregated_at !== null),
      { timeout: 150_000, intervals: [2000, 5000] },
    )
    .toBe(true);
  await page.close();
});

// erasure by session id and site reset, through the console's
// privacy page against the real stack: two visitors browse a tenant site
// through the gateway, the tenant owner erases one of them by the umami session id
// from the dashboard and then resets the whole site, and umami's own read
// api (with the tenant's sso token, through the web caddy) shows exactly
// what is left after each step.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { type BrowserContext, devices, type Page } from "@playwright/test";
import { APP, INGEST, OUT } from "../fixture/env.ts";
import {
  addSiteApi,
  api,
  captureSsoToken,
  enrollApi,
  newUser,
  openDashboard,
  pasteSnippet,
  type Site,
  signIn,
  type Tenant,
  umamiGet,
  uniqueHost,
  waitProvisioned,
  waitRegistered,
} from "./helpers.ts";
import { expect, test } from "./test.ts";

const shots = join(OUT, "privacy");

test.describe.configure({ mode: "serial" });
test.use({ timezoneId: "UTC" });

let owner: BrowserContext;
let tenant: Tenant;
let site: Site;
let host = "";
let token = "";
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
  const d = new Date();
  d.setUTCHours(0, 0, 0, 0);
  return `startAt=${d.getTime()}&endAt=${d.getTime() + 86_400_000 - 1}`;
}

// umami's session list of the site for today, read as the tenant: each
// session's id and its number of pageviews
async function sessionViews(): Promise<Map<string, number>> {
  const r = await umamiGet(
    owner,
    token,
    `/api/websites/${site.id}/sessions?${today()}&pageSize=100`,
  );
  expect(r.status, JSON.stringify(r.body)).toBe(200);
  const body = r.body as { data?: { id?: unknown; views?: unknown }[] };
  if (!Array.isArray(body.data))
    throw new Error(`umami sessions answer: ${JSON.stringify(r.body)}`);
  return new Map(body.data.map((s) => [String(s.id), Number(s.views)]));
}

async function sessions(): Promise<string[]> {
  return [...(await sessionViews()).keys()];
}

async function pageviews(): Promise<number> {
  const r = await umamiGet(owner, token, `/api/websites/${site.id}/stats?${today()}`);
  expect(r.status).toBe(200);
  const v = (r.body as Record<string, number | { value: number }>).pageviews;
  return typeof v === "number" ? v : (v?.value ?? Number.NaN);
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
  // the privacy tab of the project's settings place is the current page
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
  await expect(
    page.getByRole("navigation", { name: "Settings" }).getByRole("link", { name: "Privacy" }),
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

  const tokenP = captureSsoToken(page);
  await openDashboard(page, tenant.id, site);
  token = await tokenP;
  expect(token).not.toBe("");
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
  await form.getByLabel("Session id").fill(erased);
  const sent = page.waitForResponse(
    (r) => r.request().method() === "POST" && r.url() === `${APP}/api/tenants/${tenant.id}/erasure`,
  );
  await form.getByRole("button", { name: "Request erasure" }).click();
  expect((await sent).status()).toBe(202);
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
  const history = page.getByRole("region", { name: "Request history" });
  await expect(history.getByText(`One session: ${host}`)).toBeVisible();
  await expect(history.getByText(/Deleted 1 session\(s\), 2 event\(s\)/)).toBeVisible();

  // umami: the erased session and its pageviews are gone, the other stays
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
  await form.getByLabel("Type the site's domain to confirm").fill(`x${host}`);
  await form.getByRole("button", { name: "Reset site data" }).click();
  await expect(form.getByText(`Type ${host} exactly.`)).toBeVisible();

  await form.getByLabel("Type the site's domain to confirm").fill(host);
  await form.getByRole("button", { name: "Reset site data" }).click();
  const dialog = page.getByRole("dialog", { name: `Reset all web data of ${host}?` });
  await expect(dialog).toBeVisible();
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

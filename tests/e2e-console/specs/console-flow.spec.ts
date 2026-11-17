// console flow end to end, with the snippet: sign in through the
// oauth fixture, enroll a project, add a site, take the snippet from the
// snippet page and paste it into the example site, generate browser traffic
// through the real gateway, open the console's web dashboard and see the
// site's page views with the seven rfp metrics for a selected time range.
// the snippet's integrity equals the manifest value, and the tag loads and
// executes.
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { type BrowserContext, devices, type Page } from "@playwright/test";
import { APP, INGEST, OUT, REFERRER_HOST, REFERRER_ORIGIN } from "../fixture/env.ts";
import {
  api,
  manifest,
  newUser,
  openDashboard,
  pasteSnippet,
  restartJobs,
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

const shots = join(OUT, "flow");

test.describe.configure({ mode: "serial" });
// "today" in umami's range picker is the browser's day; utc keeps it equal
// to the utc day the counts are checked for
test.use({ timezoneId: "UTC" });

let tenant: Tenant;
let site: Site;
let tag = "";
// the tenant owner's browser, signed in once and shared by the serial tests
let owner: BrowserContext;
// a fresh tenant host per attempt
let TENANT_HOST = "";
let TENANT_ORIGIN = "";

test.beforeAll(async ({ browser }) => {
  owner = await browser.newContext({ timezoneId: "UTC" });
  TENANT_HOST = uniqueHost("shop");
  TENANT_ORIGIN = `https://${TENANT_HOST}`;
});
test.afterAll(async () => {
  await owner?.close();
});

async function shot(page: Page, name: string) {
  await page.screenshot({ path: join(shots, `${name}.png`), fullPage: true });
}

// every POST /v1/e the tracker made from a page, with the gateway's status
function recordEvents(page: Page, into: { t: string; u: string; status: number }[]) {
  page.on("response", async (r) => {
    if (r.request().method() !== "POST" || r.url() !== `${INGEST}/v1/e`) return;
    const body = JSON.parse(r.request().postData() ?? "{}") as { t?: string; u?: string };
    into.push({ t: body.t ?? "", u: body.u ?? "", status: r.status() });
  });
}

async function waitAccepted(events: { t: string; status: number }[], pageviews: number) {
  await expect
    .poll(() => events.filter((e) => e.t === "pv" && e.status === 202).length, { timeout: 30_000 })
    .toBe(pageviews);
}

test("sign in, enroll, add a site and copy the snippet", async ({ browserName }) => {
  const context = owner;
  const page = await owner.newPage();
  mkdirSync(shots, { recursive: true });
  const user = newUser("flow");
  await signIn(page, user);
  await shot(page, "01-projects");

  await page.goto(`${APP}/projects/new`);
  const name = `Flow project ${user.login}`;
  await page.getByLabel("Project name").fill(name);
  await page.getByLabel("Website").fill("https://example.org");
  await page
    .getByLabel("How does the project use Stellar?")
    .fill("Payments on testnet, measured by the console e2e");
  await page.getByRole("checkbox").check();
  await shot(page, "02-enroll");
  await page.getByRole("button", { name: "Enroll project" }).click();
  await page.waitForURL(/\/projects\/[0-9a-f-]{36}\/sites$/);
  const tenantId = new URL(page.url()).pathname.split("/")[2] ?? "";
  tenant = (await api<Tenant>(context, "GET", `/api/tenants/${tenantId}`)).body;
  expect(tenant).toMatchObject({ name, status: "active", role: "owner" });

  const form = page.getByRole("form", { name: "Add a site" });
  await form.getByLabel("Name").fill("Shop");
  await form.getByLabel("Domain").fill(TENANT_HOST);
  await form.getByLabel("Allowed origins").fill(TENANT_ORIGIN);
  await shot(page, "03-add-site");
  const created = page.waitForResponse(
    (r) => r.request().method() === "POST" && r.url() === `${APP}/api/tenants/${tenant.id}/sites`,
  );
  await form.getByRole("button", { name: "Add and set up" }).click();
  expect((await created).status()).toBe(201);
  const sites = await api<{ items: Site[] }>(context, "GET", `/api/tenants/${tenant.id}/sites`);
  expect(sites.body.items).toHaveLength(1);
  site = sites.body.items[0] as Site;
  expect(site).toMatchObject({ domain: TENANT_HOST, origins: [TENANT_ORIGIN], status: "active" });

  // adding a site leads to its setup; the snippet url is its install step:
  // the tag, the integrity shown, and the copy button
  await page.waitForURL(`${APP}/projects/${tenant.id}/sites/${site.id}/snippet`);
  await page.goto(`${APP}/projects/${tenant.id}/sites/${site.id}/snippet`);
  await expect(page.getByRole("heading", { name: `Set up ${site.name}` })).toBeVisible();
  await expect(page.getByRole("region", { name: "Install the tracker" })).toBeVisible();
  // the pinned script, integrity and site id sit under a disclosure
  await page.getByText("Version and what the tag pins").click();
  const newest = manifest().at(-1);
  expect(newest).toBeTruthy();
  const shown = (await page.locator("pre code").first().innerText()).trim();
  const integrityShown = (
    await page
      .getByText(/^sha384-/)
      .first()
      .innerText()
  ).trim();
  expect(integrityShown, "integrity on the snippet page equals the manifest").toBe(
    newest?.integrity,
  );
  const api0 = await api<{ tag: string; integrity: string }>(
    context,
    "GET",
    `/api/sites/${site.id}/snippet`,
  );
  expect(api0.body.integrity).toBe(newest?.integrity);
  expect(api0.body.tag).toBe(
    `<script defer src="${INGEST}/v/${newest?.version}/sdk.js" integrity="${newest?.integrity}" crossorigin="anonymous" data-website-id="${site.id}"></script>`,
  );
  // the static html guide shows the tag inside an example head
  expect(shown).toContain(api0.body.tag);
  // every manifest version the picker offers has the manifest's integrity
  for (const rel of manifest()) {
    const r = await api<{ integrity: string; tag: string }>(
      context,
      "GET",
      `/api/sites/${site.id}/snippet?version=${rel.version}`,
    );
    expect(r.status).toBe(200);
    expect(r.body.integrity).toBe(rel.integrity);
    expect(r.body.tag).toContain(`integrity="${rel.integrity}"`);
  }

  if (browserName === "chromium") {
    await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: APP });
  }
  await page.getByRole("button", { name: "Copy tag" }).first().click();
  await expect(page.getByRole("button", { name: "Copied" }).first()).toBeVisible();
  tag = await page.evaluate(() => navigator.clipboard.readText());
  expect(tag, "the copied text is the snippet route's tag").toBe(api0.body.tag);
  await shot(page, "04-snippet");
});

test("the pasted snippet loads, verifies and sends traffic through the gateway", async ({
  browser,
  page,
}) => {
  const context = owner;
  expect(tag).not.toBe("");
  await pasteSnippet(TENANT_HOST, tag);
  await waitProvisioned(context, tenant.id);
  await waitRegistered(context, site.id, TENANT_ORIGIN);

  // visitor 1: desktop chrome, arrives from a referring site, reads two pages
  // with a pause, so it is not a bounce and has a duration
  const events: { t: string; u: string; status: number }[] = [];
  const sdk: { status: number }[] = [];
  page.on("response", (r) => {
    if (r.url().startsWith(`${INGEST}/v/`)) sdk.push({ status: r.status() });
  });
  recordEvents(page, events);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  await page.goto(`${REFERRER_ORIGIN}/?to=${encodeURIComponent(`${TENANT_ORIGIN}/`)}`);
  await page.click("#to-shop");
  await page.waitForURL(`${TENANT_ORIGIN}/`);
  await waitAccepted(events, 1);
  // the tag executed: the tracker's api is on the page
  expect(
    await page.evaluate(
      () => typeof (window as unknown as { umami?: { track?: unknown } }).umami?.track,
    ),
  ).toBe("function");
  expect(
    sdk.every((s) => s.status === 200) && sdk.length > 0,
    `sdk responses ${JSON.stringify(sdk)}`,
  ).toBe(true);
  await page.waitForTimeout(8_000);
  await page.click("#to-about");
  await page.waitForURL(`${TENANT_ORIGIN}/about.html`);
  await waitAccepted(events, 2);
  expect(errors, "page errors (an integrity failure would show here)").toEqual([]);
  expect(events.filter((e) => e.t === "pv").map((e) => e.u)).toEqual(["/", "/about.html"]);

  // visitor 2: a phone, one page only, a bounce
  const phone: BrowserContext = await browser.newContext({
    ...devices["Pixel 7"],
    proxy: { server: test.info().project.use.proxy?.server ?? "" },
    ignoreHTTPSErrors: true,
    timezoneId: "UTC",
  });
  try {
    const p = await phone.newPage();
    const ev2: { t: string; u: string; status: number }[] = [];
    recordEvents(p, ev2);
    await p.goto(`${TENANT_ORIGIN}/`);
    await waitAccepted(ev2, 1);
  } finally {
    await phone.close();
  }

  // a tampered tag (one integrity byte changed) is refused by the browser
  const bad = tag.replace(
    /integrity="sha384-(.)/,
    (_m, c: string) => `integrity="sha384-${c === "A" ? "B" : "A"}`,
  );
  expect(bad).not.toBe(tag);
  await pasteSnippet(TENANT_HOST, bad);
  const tampered = await browser.newContext({
    ignoreHTTPSErrors: true,
    proxy: { server: test.info().project.use.proxy?.server ?? "" },
  });
  try {
    const p = await tampered.newPage();
    const ev3: { t: string; u: string; status: number }[] = [];
    recordEvents(p, ev3);
    await p.goto(`${TENANT_ORIGIN}/`);
    await p.waitForTimeout(3_000);
    expect(await p.evaluate(() => typeof (window as unknown as { umami?: unknown }).umami)).toBe(
      "undefined",
    );
    expect(ev3).toEqual([]);
  } finally {
    await tampered.close();
    await pasteSnippet(TENANT_HOST, tag);
  }
});

test("the console's web dashboard shows the site's seven metrics for a selected range", async () => {
  const context = owner;
  const page = await owner.newPage();
  // the console overview carries the db-ip attribution
  await page.goto(`${APP}/projects/${tenant.id}`);
  await expect(page.getByRole("link", { name: "IP Geolocation by DB-IP" })).toHaveAttribute(
    "href",
    "https://db-ip.com",
  );
  await shot(page, "05-overview");

  // the hours of today come from the engine as soon as the replayer
  // delivered: 3 views by 2 visitors
  await waitWebReady(context, tenant.id, site.id);
  const today = new Date().toISOString().slice(0, 10);
  type Hourly = { points: { pageviews: number; visitors: number }[] };
  await expect
    .poll(
      async () => {
        const r = await webGet<Hourly>(context, tenant.id, `hourly?site=${site.id}&day=${today}`);
        return r.body.points.reduce((a, p) => a + p.pageviews, 0);
      },
      { timeout: 120_000, intervals: [1000, 2000, 5000] },
    )
    .toBe(3);

  // the totals are the daily aggregates the operator views read; the jobs
  // daemon reads umami when it starts, so it is restarted until they hold
  // every view
  type Totals = {
    pageviews: number;
    visitors: number;
    visits: number;
    bounces: number;
    totaltime: number;
    bounce_rate: number | null;
  };
  type Summary = { current: { totals: Totals }; read_at: string | null; source: string };
  const summary = async () =>
    (await webGet<Summary>(context, tenant.id, `summary?site=${site.id}&from=${today}&to=${today}`))
      .body;
  let s = await summary();
  for (let attempt = 0; attempt < 6 && s.current.totals.pageviews !== 3; attempt++) {
    await restartJobs();
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      s = await summary();
      if (s.current.totals.pageviews === 3) break;
      await new Promise((r) => setTimeout(r, 3_000));
    }
  }
  expect(s.source).toBe("aggregate");
  expect(s.current.totals).toMatchObject({ pageviews: 3, visitors: 2, visits: 2, bounces: 1 });
  expect(s.current.totals.totaltime).toBeGreaterThanOrEqual(8);
  expect(s.current.totals.bounce_rate).toBe(0.5);
  expect(s.read_at).not.toBeNull();

  type Breakdown = { items: { key: string; value: number }[]; source: string };
  const dim = async (d: string) =>
    (
      await webGet<Breakdown>(
        context,
        tenant.id,
        `breakdown?site=${site.id}&from=${today}&to=${today}&dimension=${d}`,
      )
    ).body;
  expect((await dim("referrer")).items.map((i) => i.key)).toContain(REFERRER_HOST);
  expect((await dim("country")).items).toEqual([{ key: "DE", value: 2 }]);
  expect((await dim("browser")).items.map((i) => i.key)).toContain("chrome");
  // tracker 1.3 sends no screen size, so umami names a desktop user agent
  // "desktop" rather than splitting it into laptop and desktop by width
  expect((await dim("device")).items.map((i) => i.key).sort()).toEqual(["desktop", "mobile"]);
  const pages = await dim("page");
  expect(pages.source).toBe("engine");
  expect(pages.items.length).toBeGreaterThan(0);

  // the same numbers in the dashboard, reached from the overview's site row
  await openDashboard(page, tenant.id, site);
  await page
    .getByRole("radiogroup", { name: "Range" })
    .getByRole("radio", { name: "Today" })
    .click();
  await expect(page).toHaveURL(/range=today/);
  const totals = page.getByRole("region", { name: "Totals" });
  for (const [label, value] of [
    ["Page views", "3"],
    ["Unique visitors", "2"],
    ["Visits", "2"],
    ["Bounce rate", "50.0%"],
  ] as const) {
    await expect(totals.getByText(label, { exact: true })).toBeVisible();
    await expect(totals.getByText(value, { exact: true }).first()).toBeVisible();
  }
  await expect(totals.getByText("Visit duration", { exact: true })).toBeVisible();
  await expect(page.getByText("Page views per hour")).toBeVisible();
  await expect(page.getByLabel("Right now")).toBeVisible();
  await shot(page, "06-dashboard-top");

  for (const [heading, value] of [
    ["Sources", REFERRER_HOST],
    ["Countries", "Germany"],
    // the engine's browser key reads as the browser's name
    ["Environment", "Chrome"],
  ] as const) {
    const panel = page
      .locator("section")
      .filter({ has: page.getByRole("heading", { name: heading }) });
    await panel.scrollIntoViewIfNeeded();
    await expect(panel.getByText(value, { exact: true }).first()).toBeVisible();
  }
  const env = page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "Environment" }) });
  await env.getByRole("radio", { name: "Devices" }).click();
  await expect(env.getByText("Desktop", { exact: true })).toBeVisible();
  await expect(env.getByText("Mobile", { exact: true })).toBeVisible();
  await shot(page, "07-dashboard-panels");

  // a longer range is drawn per day from the aggregates, against the
  // previous period
  await page
    .getByRole("radiogroup", { name: "Range" })
    .getByRole("radio", { name: "7 days" })
    .click();
  await expect(page).toHaveURL(/range=7/);
  await expect(page.getByText("Page views per day")).toBeVisible();
  // a custom range is checked before it is asked for
  await page
    .getByRole("radiogroup", { name: "Range" })
    .getByRole("radio", { name: "Custom" })
    .click();
  await page.getByLabel("From (UTC day)").fill("2026-01-10");
  await page.getByLabel("To (UTC day)").fill("2026-01-01");
  await page.getByRole("button", { name: "Apply range" }).click();
  await expect(page.getByText("End on or after the start.")).toBeVisible();
  await page.getByLabel("To (UTC day)").fill(today);
  await page.getByLabel("From (UTC day)").fill(today);
  await page.getByRole("button", { name: "Apply range" }).click();
  await expect(page).toHaveURL(new RegExp(`from=${today}&to=${today}`));
  // the quarantined traffic of the site is its own view
  await page
    .getByRole("radiogroup", { name: "Traffic" })
    .getByRole("radio", { name: "Quarantined" })
    .click();
  await expect(page).toHaveURL(/band=quarantine/);
  await expect(page.getByLabel("Right now")).toHaveCount(0);

  // the dashboard carries the db-ip link, once
  await expect(page.getByRole("link", { name: "IP Geolocation by DB-IP" })).toHaveCount(1);
  writeFileSync(join(shots, "stats.json"), `${JSON.stringify(s, null, 2)}\n`);
});

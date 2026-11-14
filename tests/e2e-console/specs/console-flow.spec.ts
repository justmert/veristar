// the design verification, "end-to-end" and "snippet": sign in through the
// oauth fixture, enroll a project, add a site, take the snippet from the
// snippet page and paste it into the example site, generate browser traffic
// through the real gateway, click "Open web dashboard" and see the site's
// pageviews in umami with the seven rfp metrics for a selected time range.
// the snippet's integrity equals the manifest value, and the tag loads and
// executes.
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { type BrowserContext, devices, type Page } from "@playwright/test";
import { APP, INGEST, OUT, REFERRER_HOST, REFERRER_ORIGIN, WEB } from "../fixture/env.ts";
import {
  api,
  captureSsoToken,
  manifest,
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
    .fill("Payments on testnet, measured by this stage e2e");
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

test("open web dashboard shows the site's pageviews with the seven metrics for a selected range", async () => {
  const context = owner;
  const page = await owner.newPage();
  // the console overview links into umami and carries the db-ip attribution
  await page.goto(`${APP}/projects/${tenant.id}`);
  await expect(page.getByRole("link", { name: "IP Geolocation by DB-IP" })).toHaveAttribute(
    "href",
    "https://db-ip.com",
  );
  await shot(page, "05-overview");

  const tokenP = captureSsoToken(page);
  await openDashboard(page, tenant.id, site);
  await page.waitForURL(`${WEB}/websites/${site.id}**`);
  const token = await tokenP;

  // the numbers, read with the tenant's own token through the web caddy:
  // 3 views, 2 visitors, 2 visits, 1 bounce, a duration of at least the
  // pause. the replayer delivers within seconds; poll until it has.
  const day = new Date();
  day.setUTCHours(0, 0, 0, 0);
  const range = `startAt=${day.getTime()}&endAt=${day.getTime() + 86_400_000 - 1}`;
  type Stats = Record<string, number | { value: number }>;
  const val = (s: Stats, k: string) => {
    const v = s[k];
    return typeof v === "number" ? v : (v?.value ?? Number.NaN);
  };
  await expect
    .poll(
      async () =>
        val(
          (await umamiGet(context, token, `/api/websites/${site.id}/stats?${range}`)).body as Stats,
          "pageviews",
        ),
      {
        timeout: 120_000,
        intervals: [1000, 2000, 5000],
      },
    )
    .toBe(3);
  const stats = (await umamiGet(context, token, `/api/websites/${site.id}/stats?${range}`))
    .body as Stats;
  expect(val(stats, "visitors")).toBe(2);
  expect(val(stats, "visits")).toBe(2);
  expect(val(stats, "bounces")).toBe(1);
  expect(val(stats, "totaltime")).toBeGreaterThanOrEqual(8);
  const metric = async (type: string) =>
    (await umamiGet(context, token, `/api/websites/${site.id}/metrics?${range}&type=${type}`))
      .body as {
      x: string;
      y: number;
    }[];
  expect((await metric("referrer")).map((m) => m.x)).toContain(REFERRER_HOST);
  expect(await metric("country")).toEqual([{ x: "DE", y: 2 }]);
  expect((await metric("browser")).map((m) => m.x)).toContain("chrome");
  // umami names a 1280 px wide desktop screen "laptop"
  expect((await metric("device")).map((m) => m.x).sort()).toEqual(["laptop", "mobile"]);

  // the same in the ui, for the range picked in umami's date filter
  await page.reload();
  // umami's date filter is a react-aria select: a listbox trigger whose text
  // is the current range
  const dateFilter = page
    .locator('button[aria-haspopup="listbox"]')
    .filter({ visible: true })
    .filter({ hasText: /^(Last 24 hours|Today)$/ });
  await expect(dateFilter).toHaveText("Last 24 hours", { timeout: 30_000 });
  await dateFilter.click();
  await page.getByRole("option", { name: "Today", exact: true }).click();
  await expect(dateFilter).toHaveText("Today");
  const bar = async (label: string) => {
    const el = page.getByText(label, { exact: true }).first();
    await expect(el).toBeVisible();
    return el;
  };
  for (const label of ["Visitors", "Visits", "Views", "Bounce rate", "Visit duration"])
    await bar(label);
  await expect(page.getByText("50%", { exact: true }).first()).toBeVisible();
  await shot(page, "06-dashboard-top");
  for (const [heading, value] of [
    ["Sources", REFERRER_HOST],
    ["Location", "Germany"],
    ["Environment", "Chrome"],
  ] as const) {
    await page.getByRole("heading", { name: heading }).scrollIntoViewIfNeeded();
    await expect(page.getByText(value, { exact: true }).first()).toBeVisible();
  }
  await page.getByRole("tab", { name: "Devices" }).click();
  await expect(page.getByText("Laptop", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("Mobile", { exact: true }).first()).toBeVisible();
  await shot(page, "07-dashboard-panels");

  // umami's pages carry the db-ip link too (the web caddy injects it)
  await expect(page.getByRole("link", { name: "IP Geolocation by DB-IP" })).toHaveCount(1);
  writeFileSync(join(shots, "stats.json"), `${JSON.stringify(stats, null, 2)}\n`);
});

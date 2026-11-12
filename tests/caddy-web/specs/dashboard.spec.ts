import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, type Page, test } from "@playwright/test";

// a view-only tenant opens its website dashboard through the web caddy the way
// the console sends it there: a same-site page links to /sso. every
// request the ui makes goes through caddy's allowlist and deny list,
// and none of them may be refused.

const port = process.env.WEB_PORT ?? "";
const domain = process.env.WEB_DOMAIN ?? "";
const token = process.env.WEB_TOKEN ?? "";
const website = process.env.WEB_WEBSITE ?? "";
const team = process.env.WEB_TEAM ?? "";
const web = `https://web.${domain}:${port}`;
const app = `https://app.${domain}:${port}`;
const here = dirname(fileURLToPath(import.meta.url));
// screenshots are verification output, kept in the gitignored scratch area
const shots = resolve(here, "../../../.scratch/scratch/caddy-web");

async function shot(page: Page, name: string) {
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

// the rfp metrics on the overview: views, visitors, visits, bounce rate and
// visit duration in the metrics bar, the pageviews chart, then referrers,
// countries, browsers and devices in the panels. the go side sent one
// pageview from france with a referrer, in chrome on linux.
async function checkDashboard(page: Page, name: string) {
  for (const label of ["Visitors", "Visits", "Views", "Bounce rate", "Visit duration"]) {
    await expect(page.getByText(label, { exact: true }).first()).toBeVisible({ timeout: 30_000 });
  }
  await shot(page, `${name}-metrics-and-pageviews`);
  for (const [heading, value] of [
    ["Sources", "ref-caddy.example"],
    ["Location", "France"],
    ["Environment", "Chrome"],
  ] as const) {
    await page.getByRole("heading", { name: heading }).scrollIntoViewIfNeeded();
    await expect(page.getByText(value, { exact: true }).first()).toBeVisible({ timeout: 20_000 });
    await shot(page, `${name}-${heading.toLowerCase()}`);
  }
  await page.getByRole("tab", { name: "Devices" }).click();
  await expect(page.getByText("Desktop", { exact: true }).first()).toBeVisible({ timeout: 20_000 });
  await shot(page, `${name}-devices`);
}

// chromium aborts every request in flight with ERR_NETWORK_CHANGED when the
// host's interfaces change, which other docker work on a shared machine does
// all the time. that is not an answer from caddy or umami, so a page whose
// check failed while such aborts happened is loaded again; any other failure,
// and every refusal, still fails the test.
let networkChanged = 0;
const retries: string[] = [];

async function retrying(page: Page, name: string, check: () => Promise<void>) {
  for (let attempt = 1; ; attempt++) {
    const before = networkChanged;
    try {
      await check();
      return;
    } catch (e) {
      if (networkChanged === before || attempt >= 5) throw e;
      retries.push(`${name}: attempt ${attempt} lost requests to ERR_NETWORK_CHANGED`);
      await page.reload();
    }
  }
}

const expectDashboard = (page: Page, name: string) =>
  retrying(page, name, () => checkDashboard(page, name));

test("tenant dashboard through the web caddy", async ({ page, context }) => {
  mkdirSync(shots, { recursive: true });
  const refused: string[] = [];
  const failed: string[] = [];
  const statuses: Record<string, number[]> = {};
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("requestfailed", (r) => {
    if (r.failure()?.errorText.includes("ERR_NETWORK_CHANGED")) networkChanged++;
  });
  page.on("response", (r) => {
    const u = new URL(r.url());
    if (u.hostname !== `web.${domain}`) return;
    const key = `${r.request().method()} ${u.pathname.replace(/[0-9a-f]{8}-[0-9a-f-]{27}/g, "{id}")}`;
    const seen = statuses[key] ?? [];
    seen.push(r.status());
    statuses[key] = seen;
    if (r.status() === 403) refused.push(key);
    else if (r.status() >= 400) failed.push(`${key} ${r.status()}`);
  });

  // a navigation typed into the address bar carries Sec-Fetch-Site: none and
  // is refused, so a link from another site cannot log a browser in
  const direct = await page.goto(
    `${web}/sso?token=${encodeURIComponent(token)}&url=${encodeURIComponent(`/websites/${website}`)}`,
  );
  expect(direct?.status()).toBe(403);
  refused.length = 0;
  delete statuses["GET /sso"];

  // the console stands in as one page on app.<domain>, same site as web.<domain>
  const sso = `${web}/sso?token=${encodeURIComponent(token)}&url=${encodeURIComponent(`/websites/${website}`)}`;
  await context.route(`${app}/**`, (r) =>
    r.fulfill({ contentType: "text/html", body: `<a id="go" href="${sso}">open dashboard</a>` }),
  );
  await retrying(page, "00-sso", async () => {
    await page.goto(`${app}/console`);
    await page.click("#go");
    await page.waitForURL(`**/websites/${website}`, { timeout: 30_000 });
  });
  await expectDashboard(page, "01-overview");

  // umami's own team navigation and the team website list, which use the
  // /api/users/<id>/teams and /websites reads
  await page.goto(`/teams/${team}/websites/${website}`);
  await expectDashboard(page, "02-team-overview");
  await page.goto(`/teams/${team}/websites`);
  await retrying(page, "03-team-websites", () =>
    expect(page.getByText("web-caddy", { exact: true }).first()).toBeVisible({ timeout: 20_000 }),
  );
  await shot(page, "03-team-websites");

  // the teams page lists the caller's teams through /api/users/<id>/teams
  await page.goto("/settings/teams");
  await retrying(page, "07-settings-teams", () =>
    expect(page.getByText("web-caddy", { exact: true }).first()).toBeVisible({ timeout: 20_000 }),
  );
  await shot(page, "07-settings-teams");

  // the other report pages a tenant opens from the overview
  for (const [n, path] of [
    ["04-realtime", `/websites/${website}/realtime`],
    ["05-sessions", `/websites/${website}/sessions`],
    ["06-events", `/websites/${website}/events`],
  ] as const) {
    await page.goto(path);
    await page.waitForLoadState("networkidle");
    await shot(page, n);
  }

  writeFileSync(
    `${shots}/notes.json`,
    `${JSON.stringify({ statuses, failed, refused, errors, retries, networkChanged }, null, 2)}\n`,
  );
  // umami's layout always loads /telemetry.js (src/app/(main)/App.tsx). the
  // the design deny list refuses it on purpose; with DISABLE_TELEMETRY umami
  // would only answer an empty comment, so the page loses nothing.
  expect(
    refused.filter((r) => r !== "GET /telemetry.js"),
    "requests caddy refused",
  ).toEqual([]);
  expect(
    failed.filter((f) => / 5\d\d$/.test(f)),
    "server errors",
  ).toEqual([]);
  for (const k of [
    "GET /api/websites/{id}/stats",
    "GET /api/websites/{id}/pageviews",
    "GET /api/websites/{id}/metrics",
    "GET /api/users/{id}/teams",
    "GET /api/users/{id}/websites",
  ]) {
    expect(statuses[k]?.length ?? 0, `${k} was called`).toBeGreaterThan(0);
    expect(
      statuses[k]?.every((s) => s === 200),
      `${k} answered ${statuses[k]}`,
    ).toBe(true);
  }
  expect(errors, "page errors").toEqual([]);
});

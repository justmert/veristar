import { expect, type Page, test } from "@playwright/test";

// db-ip's country lite licence asks for a link back on every page that shows
// results from the database. umami runs unmodified, so the web caddy adds
// "IP Geolocation by DB-IP" before </body> of every html answer
// (deploy/caddy/web.Caddyfile). this checks that
// the link is on every tenant dashboard page, exactly once, after full loads
// and after umami's client-side navigation, which never reloads the document.
// if react dropped the node, this is the test that fails, and the injection
// approach has to be reconsidered rather than worked around silently.

const port = process.env.WEB_PORT ?? "";
const domain = process.env.WEB_DOMAIN ?? "";
const token = process.env.WEB_TOKEN ?? "";
const website = process.env.WEB_WEBSITE ?? "";
const team = process.env.WEB_TEAM ?? "";
const web = `https://web.${domain}:${port}`;
const app = `https://app.${domain}:${port}`;

const linkText = "IP Geolocation by DB-IP";

async function expectAttribution(page: Page, where: string) {
  const link = page.locator('a[href="https://db-ip.com"]');
  await expect(link, `${where}: one attribution link`).toHaveCount(1, { timeout: 20_000 });
  await expect(link, `${where}: link text`).toHaveText(linkText);
  await expect(link, `${where}: link visible`).toBeVisible();
  // the link sits on top of the page, not under umami's layout
  const onTop = await link.evaluate((a) => {
    const r = a.getBoundingClientRect();
    const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return hit === a || a.contains(hit);
  });
  expect(onTop, `${where}: link is not covered`).toBe(true);
}

// marks the current document; a client-side navigation keeps the mark, a
// full load loses it
async function markDocument(page: Page): Promise<string> {
  const mark = `${Date.now()}-${Math.random()}`;
  await page.evaluate((m) => {
    (window as unknown as { __saDoc: string }).__saDoc = m;
  }, mark);
  return mark;
}

async function documentMark(page: Page): Promise<string | undefined> {
  return page.evaluate(() => (window as unknown as { __saDoc?: string }).__saDoc);
}

// chromium aborts requests in flight with ERR_NETWORK_CHANGED when the host's
// interfaces change, which other docker work on a shared machine causes (the
// same handling as dashboard.spec.ts). a step that failed while such aborts
// happened is run again from its own start; any other failure fails the test.
let networkChanged = 0;
const retries: string[] = [];

async function step(name: string, fn: () => Promise<void>) {
  for (let attempt = 1; ; attempt++) {
    const before = networkChanged;
    try {
      await fn();
      return;
    } catch (e) {
      if (networkChanged === before || attempt >= 5) throw e;
      retries.push(`${name}: attempt ${attempt} lost requests to ERR_NETWORK_CHANGED`);
    }
  }
}

test("db-ip attribution on every dashboard page", async ({ page, context }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("requestfailed", (r) => {
    if (r.failure()?.errorText.includes("ERR_NETWORK_CHANGED")) networkChanged++;
  });

  // the raw html from caddy carries the link exactly once, before </body>
  const sso = `${web}/sso?token=${encodeURIComponent(token)}&url=${encodeURIComponent(`/websites/${website}`)}`;
  await context.route(`${app}/**`, (r) =>
    r.fulfill({ contentType: "text/html", body: `<a id="go" href="${sso}">open dashboard</a>` }),
  );
  await step("sso", async () => {
    await page.goto(`${app}/console`);
    await page.click("#go");
    await page.waitForURL(`**/websites/${website}`, { timeout: 30_000 });
    await expect(page.getByText("Visitors", { exact: true }).first()).toBeVisible({
      timeout: 30_000,
    });
    await expectAttribution(page, "overview after sso");
  });

  // fetched from inside the page, so it goes through chromium's host mapping
  // to caddy like every other request
  const { status, html } = await page.evaluate(async (path) => {
    const r = await fetch(path, { headers: { Accept: "text/html" }, cache: "no-store" });
    return { status: r.status, html: await r.text() };
  }, `/websites/${website}`);
  expect(status).toBe(200);
  expect(html.split(linkText).length - 1, "link occurrences in the html").toBe(1);
  expect(html.split("</body>").length - 1, "</body> occurrences in the html").toBe(1);
  expect(html.indexOf(linkText)).toBeLessThan(html.indexOf("</body>"));

  // every page of the website navigation, reached by clicking umami's own
  // links: the document must stay the same and keep the link
  const visited: string[] = [];
  await step("client-side navigation", async () => {
    visited.length = 0;
    await page.goto(`/websites/${website}`);
    await expect(page.getByText("Visitors", { exact: true }).first()).toBeVisible({
      timeout: 30_000,
    });
    const navHrefs = await page
      .locator(`a[href*="/websites/${website}/"]`)
      .evaluateAll((as) => [
        ...new Set(as.map((a) => (a as HTMLAnchorElement).getAttribute("href") ?? "")),
      ]);
    // hrefs may carry umami's query state; the path names the page
    const pathOf = (h: string) => new URL(h, web).pathname;
    const sections = navHrefs.filter(
      (h) => /\/websites\/[0-9a-f-]+\/[a-z-]+$/.test(pathOf(h)) && !pathOf(h).endsWith("/settings"),
    );
    for (const want of ["/events", "/sessions", "/compare", "/breakdown", "/realtime"]) {
      expect(
        sections.some((h) => pathOf(h).endsWith(want)),
        `website navigation links to ${want} (found ${sections.join(", ")})`,
      ).toBe(true);
    }
    const mark = await markDocument(page);
    for (const href of sections) {
      await page.locator(`a[href="${href}"]`).first().click();
      await page.waitForURL((u) => u.pathname === pathOf(href), { timeout: 30_000 });
      await page.waitForLoadState("networkidle");
      expect(await documentMark(page), `${href} was reached without a full reload`).toBe(mark);
      await expectAttribution(page, `client-side ${href}`);
      visited.push(href);
    }
    // and back to the overview
    await page
      .locator(`a[href*="/websites/${website}"]`)
      .filter({ hasText: /^Overview$/ })
      .first()
      .click();
    await page.waitForURL((u) => u.pathname.endsWith(`/websites/${website}`), { timeout: 30_000 });
    expect(await documentMark(page), "overview was reached without a full reload").toBe(mark);
    await expectAttribution(page, "client-side overview");
  });

  // full loads of the other pages a tenant reaches: team views and settings
  for (const path of [
    `/websites/${website}`,
    `/websites/${website}/realtime`,
    `/websites/${website}/sessions`,
    `/teams/${team}/websites/${website}`,
    `/teams/${team}/websites`,
    `/settings/teams`,
    `/websites/${website}/settings`,
  ]) {
    await step(`full load ${path}`, async () => {
      await page.goto(path);
      await page.waitForLoadState("networkidle");
      await expectAttribution(page, `full load ${path}`);
    });
  }

  // a reload of a page reached client-side gets a fresh document with the link
  await step("reload", async () => {
    await page.reload();
    await page.waitForLoadState("networkidle");
    await expectAttribution(page, "reload");
  });
  if (retries.length) console.log(retries.join("\n"));

  expect(visited.length, "pages visited client-side").toBeGreaterThanOrEqual(5);
  expect(errors, "page errors").toEqual([]);
});

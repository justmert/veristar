// browser scenarios of the this stage web path suite. each test only drives the
// browser and checks that the real gateway answered every event with 202;
// what umami stored is checked by the go test (tests/webpath), which selects
// a scenario with --grep on its tag and passes parameters as WEBPATH_* env.
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { devices, expect, test } from "@playwright/test";
import { type SiteDef, siteByKey } from "../../sites.ts";
import {
  customEvents,
  eventsFor,
  expectContract,
  expectCustomEvents,
  expectPageviews,
  recorded,
  resetFixture,
  trackerReady,
} from "../helpers.ts";

function env(name: string): string {
  const v = process.env[name];
  if (v === undefined || v === "") throw new Error(`missing env ${name}`);
  return v;
}

function envInt(name: string): number {
  const n = Number(env(name));
  if (!Number.isInteger(n) || n < 0) throw new Error(`env ${name} must be a non-negative integer`);
  return n;
}

function siteFromEnv(): SiteDef {
  return siteByKey(env("WEBPATH_SITE") as SiteDef["key"]);
}

test.beforeEach(async () => {
  await resetFixture();
});

// pageviews, visitors and visits: a first visit with three pages
test("static first visit, three pages @s1-visit1", async ({ page }) => {
  const site = siteByKey("static-html");
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await page.click("#to-about");
  await page.waitForURL(`${site.origin}/about.html`);
  await trackerReady(page);
  await expectPageviews(site, 2);
  await page.click("#to-home");
  await page.waitForURL(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 3);
  await expectContract(site);
});

// a second profile with one page: one visitor, one visit, one bounce
test("static single page @s1-single", async ({ page }) => {
  const site = siteByKey("static-html");
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await expectContract(site);
});

// the first profile again after the gap: a second visit with two pages
test("static second visit, two pages @s1-visit2", async ({ page }) => {
  const site = siteByKey("static-html");
  await page.goto(`${site.origin}/about.html`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await page.click("#to-home");
  await page.waitForURL(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 2);
  await expectContract(site);
});

// session duration: three route changes ten seconds apart
test("spa visit with controlled waits @duration", async ({ page }) => {
  const site = siteByKey("vite-react-spa");
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await page.waitForTimeout(10_000);
  await page.click("#nav-pricing");
  await page.waitForURL(`${site.origin}/pricing`);
  await expectPageviews(site, 2);
  await page.waitForTimeout(10_000);
  await page.click("#nav-docs");
  await page.waitForURL(`${site.origin}/docs/getting-started`);
  await expectPageviews(site, 3);
  await expectContract(site);
});

// referrers: a page on another domain links to the site. the referring page
// is served by playwright itself, so it never reaches the network.
const REFERRER_PAGE = "https://news.sa-referrer.example/story/42";
test("navigation from a referring page @referrer", async ({ page }) => {
  const site = siteByKey("react-router-ssr");
  await page.route(REFERRER_PAGE, (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/html; charset=utf-8",
      body: `<!doctype html><title>news</title><a id="go" href="${site.origin}/">read more</a>`,
    }),
  );
  await page.goto(REFERRER_PAGE);
  await page.click("#go");
  await page.waitForURL(`${site.origin}/`);
  await trackerReady(page);
  const pvs = await expectPageviews(site, 1);
  // browsers send the origin only to another site (strict-origin-when-cross-origin)
  expect(pvs[0]?.r).toMatch(/^https:\/\/news\.sa-referrer\.example\//);
  await expectContract(site);
});

// one page load on the site the go test names; used for country (with the
// forwarder switched between countries) and for device, browser and os
test("one page load @one-page", async ({ page }) => {
  const site = siteFromEnv();
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await expectContract(site);
});

// bots: a crawler user agent in a real browser. the gateway accepts the
// events (it has no bot list); umami's isbot check refuses them.
test.describe("crawler", () => {
  test.use({
    userAgent: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
  });
  test("crawler page load and click @bot", async ({ page }) => {
    const site = siteByKey("astro-ssr");
    await page.goto(`${site.origin}/`);
    await trackerReady(page);
    await expectPageviews(site, 1);
    await page.click("#manual");
    await expectCustomEvents(site, 1);
    await expectContract(site);
  });
});

// a burst of events for the outage and restart checks: WEBPATH_PAGES page
// loads, each with its pageview and WEBPATH_PER_PAGE - 1 umami.track() calls
// named <tag>-<project>-<page>-<i>, WEBPATH_PACE_MS apart, which keeps one
// prefix under the gateway's per-prefix rate (20/s) and one page load under
// the replay limit (50 events per proof)
test("burst of browser events @burst", async ({ page }, info) => {
  const site = siteFromEnv();
  const pages = envInt("WEBPATH_PAGES");
  const perPage = envInt("WEBPATH_PER_PAGE");
  const pace = envInt("WEBPATH_PACE_MS");
  const tag = env("WEBPATH_TAG");
  // what the browser itself reports about requests that did not complete,
  // so a missing event can be told apart from one the gateway refused
  const failed: string[] = [];
  page.on("requestfailed", (r) => {
    failed.push(`${r.method()} ${r.url()} ${r.failure()?.errorText ?? ""} ${r.postData() ?? ""}`);
  });
  page.on("console", (m) => {
    if (m.type() === "error" || m.type() === "warning")
      failed.push(`console ${m.type()}: ${m.text()}`);
  });
  for (let p = 0; p < pages; p++) {
    await resetFixture();
    await page.goto(`${site.origin}/`);
    await trackerReady(page);
    await expectPageviews(site, 1);
    const sent: string[] = [];
    for (let i = 1; i < perPage; i++) {
      const name = `${tag}-${info.project.name}-${p}-${i}`;
      sent.push(name);
      await page.evaluate((n) => window.umami?.track(n), name);
      await page.waitForTimeout(pace);
    }
    // a miss (an event that never reached the gateway) is recorded for the
    // go test and the burst goes on, so the outage check still runs on every
    // event the gateway accepted
    let seen: string[] = [];
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
      seen = (await customEvents(site)).map((e) => e.n ?? "");
      if (seen.length >= perPage - 1) break;
      await page.waitForTimeout(250);
    }
    const missing = sent.filter((n) => !seen.includes(n));
    if (missing.length > 0) {
      appendFileSync(
        join(env("WEBPATH_OUT"), "misses.jsonl"),
        `${JSON.stringify({ project: info.project.name, page: p, missing, browser: failed.splice(0) })}\n`,
      );
    }
    const statuses = eventsFor(await recorded(), site).map((r) => r.verdict.status);
    expect(statuses.length).toBe(perPage - missing.length);
    expect(statuses.every((s) => s === 202)).toBe(true);
  }
});

// quarantine: 22 profiles with distinct user agents from one address on one
// site. the gateway quarantines from the 20th distinct visitor token
// (IPCLUSTER_THRESHOLD 20). each profile sends a pageview and an event named
// q-<n>, so the go test can tell which profile landed in which website.
const chromeUA = devices["Desktop Chrome"].userAgent;
for (let n = 1; n <= 22; n++) {
  test.describe(`quarantine profile ${n}`, () => {
    test.use({ userAgent: `${chromeUA} sa-q/${n}` });
    test(`profile ${n} @quarantine`, async ({ page }) => {
      const site = siteByKey("vite-react-spa");
      await page.goto(`${site.origin}/`);
      await trackerReady(page);
      await expectPageviews(site, 1);
      await page.evaluate((name) => window.umami?.track(name), `q-${n}`);
      await expectCustomEvents(site, 1);
      await expectContract(site);
    });
  });
}

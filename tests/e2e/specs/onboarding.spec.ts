// behaviours the onboarding guides promise and the other specs do
// not cover: deferred start for consent, a self-hosted sdk.js, the content
// security policy of docs/onboarding/web/content-security-policy.md, and an
// external referrer kept on the first pageview of every example site
import { expect, test } from "@playwright/test";
import { SDK_PATH, SITES, siteByKey } from "../sites.ts";
import {
  expectContract,
  expectCustomEvents,
  expectPageviews,
  recorded,
  resetFixture,
  SETTLE_MS,
  trackerReady,
} from "./helpers.ts";

test.beforeEach(async () => {
  await resetFixture();
});

const staticSite = siteByKey("static-html");

test("consent: nothing is sent before umami.start(), then pageviews as usual", async ({ page }) => {
  await page.goto(`${staticSite.origin}/consent.html`);
  await trackerReady(page);
  await new Promise((r) => setTimeout(r, SETTLE_MS));
  // sdk.js loaded, but no challenge and no event before consent
  const before = await recorded();
  expect(before.filter((r) => r.path === SDK_PATH).length).toBe(1);
  expect(before.filter((r) => r.path === "/v1/c" || r.path === "/v1/e")).toEqual([]);

  await page.click("#consent-accept");
  const [pv] = await expectPageviews(staticSite, 1);
  expect(pv?.u).toBe("/consent.html");

  // the stored choice starts the tracker on the next load without a click
  await page.reload();
  await trackerReady(page);
  await expectPageviews(staticSite, 2);
  await expectContract(staticSite);
});

test("consent: a declined visitor sends nothing", async ({ page }) => {
  await page.goto(`${staticSite.origin}/consent.html`);
  await trackerReady(page);
  await page.click("#consent-decline");
  await page.reload();
  await trackerReady(page);
  await new Promise((r) => setTimeout(r, SETTLE_MS));
  const all = await recorded();
  expect(all.filter((r) => r.path === "/v1/c" || r.path === "/v1/e")).toEqual([]);
});

test("opt-out: after the flag is set nothing more is sent", async ({ page }) => {
  await page.goto(`${staticSite.origin}/`);
  await trackerReady(page);
  await expectPageviews(staticSite, 1);
  await page.click("#opt-out");
  await page.click("#signup");
  await page.reload();
  await trackerReady(page);
  await page.click("#manual");
  await new Promise((r) => setTimeout(r, SETTLE_MS));
  const events = (await recorded()).filter((r) => r.path === "/v1/e");
  expect(events.length).toBe(1);
});

test("self-hosted sdk.js: loaded from the site, events go to the gateway", async ({ page }) => {
  const sdk: string[] = [];
  page.on("response", (res) => {
    if (res.url().endsWith("/sdk.js")) sdk.push(`${res.status()} ${res.url()}`);
  });
  await page.goto(`${staticSite.origin}/self-hosted.html`);
  await trackerReady(page);
  const [pv] = await expectPageviews(staticSite, 1);
  expect(pv?.u).toBe("/self-hosted.html");
  expect(sdk).toEqual([`200 ${staticSite.origin}/sdk/1.0.0/sdk.js`]);
  // the gateway never served the file for this page
  expect((await recorded()).filter((r) => r.path === SDK_PATH)).toEqual([]);
  await expectContract(staticSite);
});

test("content security policy: the recommended policy blocks nothing", async ({ page }) => {
  await page.addInitScript(() => {
    const seen: string[] = [];
    (window as unknown as { __cspViolations: string[] }).__cspViolations = seen;
    document.addEventListener("securitypolicyviolation", (e) =>
      seen.push(`${e.effectiveDirective} ${e.blockedURI}`),
    );
  });
  const res = await page.goto(`${staticSite.origin}/`);
  expect(res?.headers()["content-security-policy"]).toContain("connect-src 'self' https://");
  await trackerReady(page);
  await expectPageviews(staticSite, 1);
  await page.click("#signup");
  await page.click("#manual");
  await expectCustomEvents(staticSite, 2);
  expect(
    await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations),
  ).toEqual([]);
  await expectContract(staticSite);
});

for (const site of SITES) {
  test(`${site.key}: the first pageview keeps an external referrer`, async ({ page }) => {
    // arrive from another site by a real navigation, so document.referrer is
    // set the way a browser sets it (origin only, cross-site default policy)
    const from = site.key === "static-html" ? siteByKey("astro-ssr") : staticSite;
    await page.goto(`${from.origin}${from.key === "static-html" ? "/about.html" : "/about"}`);
    await page.waitForLoadState("load");
    await page.evaluate((url) => {
      window.location.href = url;
    }, `${site.origin}/`);
    await page.waitForURL(`${site.origin}/`);
    await trackerReady(page);
    const [pv] = await expectPageviews(site, 1);
    expect(pv?.r).toBe(`${from.origin}/`);
  });
}

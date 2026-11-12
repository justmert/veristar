// one pageview per navigation: full page loads on the static and server
// rendered sites, route changes plus back and forward in the client-routed ones
import { expect, test } from "@playwright/test";
import { siteByKey } from "../sites.ts";
import { expectContract, expectPageviews, resetFixture, trackerReady } from "./helpers.ts";

test.beforeEach(async () => {
  await resetFixture();
});

test("static-html: each full page load is one pageview", async ({ page }) => {
  const site = siteByKey("static-html");
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  // an anchor jump fires popstate but is not a pageview: only #/ fragments count
  await page.click("#jump");
  await page.waitForURL(`${site.origin}/#details`);
  await expectPageviews(site, 1);
  await page.click("#to-about");
  await page.waitForURL(`${site.origin}/about.html`);
  await trackerReady(page);
  // the pageview waits for the page's challenge; leaving before it returns
  // cancels it (seen against the real gateway, where the round trip goes
  // through caddy), so the test waits for the about pageview first
  await expectPageviews(site, 2);
  await page.click("#to-home");
  await page.waitForURL(`${site.origin}/`);
  const pvs = await expectPageviews(site, 3);
  expect(pvs.map((e) => e.u)).toEqual(["/", "/about.html", "/"]);
  expect(pvs.map((e) => e.ti)).toEqual([
    "Static example: home",
    "Static example: about",
    "Static example: home",
  ]);
  // same-origin referrers are sent as a path
  expect(pvs[1]?.r).toBe("/");
  await expectContract(site);
});

for (const key of ["vite-react-spa", "react-router-ssr"] as const) {
  test(`${key}: route changes, back and forward are one pageview each`, async ({ page }) => {
    const site = siteByKey(key);
    await page.goto(`${site.origin}/`);
    await trackerReady(page);
    await expectPageviews(site, 1);

    await page.click("#nav-pricing");
    await page.waitForURL(`${site.origin}/pricing`);
    await expectPageviews(site, 2);

    await page.click("#nav-docs");
    await page.waitForURL(`${site.origin}/docs/getting-started`);
    await expectPageviews(site, 3);

    // popstate, which umami's tracker did not count
    await page.goBack();
    await page.waitForURL(`${site.origin}/pricing`);
    await expectPageviews(site, 4);

    await page.goForward();
    await page.waitForURL(`${site.origin}/docs/getting-started`);
    const pvs = await expectPageviews(site, 5);

    expect(pvs.map((e) => e.u)).toEqual([
      "/",
      "/pricing",
      "/docs/getting-started",
      "/pricing",
      "/docs/getting-started",
    ]);
    // each client-side pageview names the previous page as its referrer
    expect(pvs.slice(1).map((e) => e.r)).toEqual([
      "/",
      "/pricing",
      "/docs/getting-started",
      "/pricing",
    ]);
    // one challenge per page load, reused by every event of that load
    expect(new Set(pvs.map((e) => e.p.salt)).size).toBe(1);
    await expectContract(site);
  });
}

test("react-router-ssr: server rendered loads are one pageview each", async ({ page }) => {
  const site = siteByKey("react-router-ssr");
  await page.goto(`${site.origin}/docs/intro`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await page.reload();
  await trackerReady(page);
  await expectPageviews(site, 2);
  await page.goto(`${site.origin}/pricing`);
  await trackerReady(page);
  const pvs = await expectPageviews(site, 3);
  expect(pvs.map((e) => e.u)).toEqual(["/docs/intro", "/docs/intro", "/pricing"]);
  // a full load fetches a fresh challenge, so three loads use three salts
  expect(new Set(pvs.map((e) => e.p.salt)).size).toBe(3);
  await expectContract(site);
});

test("astro-ssr: server rendered loads are one pageview each", async ({ page }) => {
  const site = siteByKey("astro-ssr");
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await page.click("#nav-about");
  await page.waitForURL(`${site.origin}/about`);
  await trackerReady(page);
  await expectPageviews(site, 2);
  await page.click("#nav-docs");
  await page.waitForURL(`${site.origin}/docs/getting-started`);
  await trackerReady(page);
  const pvs = await expectPageviews(site, 3);
  expect(pvs.map((e) => e.u)).toEqual(["/", "/about", "/docs/getting-started"]);
  expect(pvs.map((e) => e.ti)).toEqual([
    "Astro example: home",
    "Astro example: about",
    "Astro example: getting-started",
  ]);
  await expectContract(site);
});

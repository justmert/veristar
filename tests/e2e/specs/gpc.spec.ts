// with global privacy control on (firefox preference), the tracker loads but
// sends nothing: no challenge request and no event
import { expect, test } from "@playwright/test";
import { SDK_PATH, SITES } from "../sites.ts";
import { recorded, resetFixture, SETTLE_MS } from "./helpers.ts";

test.beforeEach(async () => {
  await resetFixture();
});

for (const site of SITES) {
  test(`${site.key}: gpc sends nothing`, async ({ page }) => {
    await page.goto(`${site.origin}/`);
    expect(await page.evaluate(() => navigator.globalPrivacyControl)).toBe(true);
    // the tracker did load and run; only sending is off
    await page.waitForFunction(() => typeof window.umami?.track === "function");

    // a navigation, then both kinds of custom event
    await page.click(site.navSelector);
    // a client-side router (next.js) is still navigating when the click
    // returns, and "load" already fired; a goto started then is aborted by
    // that navigation (NS_BINDING_ABORTED in firefox), so wait for the url
    await page.waitForURL((u) => u.pathname !== "/");
    await page.waitForLoadState("load");
    // mintlify's router is still rendering the new route after the url
    // changed (its dev server keeps a socket.io poll open, so there is no
    // network idle to wait for), and a goto on that page would be aborted
    // again: the home page opens in a new page of the same context, with the
    // same gpc preference, while the first one closes
    const home = await page.context().newPage();
    await page.close();
    await home.goto(`${site.origin}/`);
    await home.waitForFunction(() => typeof window.umami?.track === "function");
    await home.click("#signup");
    await home.click("#manual");
    await new Promise((r) => setTimeout(r, SETTLE_MS));

    const all = await recorded();
    expect(all.filter((r) => r.path === SDK_PATH).length).toBeGreaterThan(0);
    expect(all.filter((r) => r.path === "/v1/c" || r.path === "/v1/e")).toEqual([]);
  });
}

declare global {
  interface Navigator {
    globalPrivacyControl?: boolean;
  }
}

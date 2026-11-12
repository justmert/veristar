// a tampered sdk.js served under the original integrity value must not run
import { expect, test } from "@playwright/test";
import { SITES, TRACKER_VERSION } from "../sites.ts";
import {
  expectPageviews,
  recorded,
  resetFixture,
  SETTLE_MS,
  setTampered,
  trackerReady,
} from "./helpers.ts";

test.beforeEach(async () => {
  await resetFixture();
});

test.afterEach(async () => {
  await setTampered(TRACKER_VERSION, false);
});

for (const site of SITES) {
  test(`${site.key}: tampered sdk.js is blocked by integrity`, async ({ page }) => {
    await setTampered(TRACKER_VERSION, true);
    await page.goto(`${site.origin}/`);
    await expect
      .poll(
        async () => (await recorded()).filter((r) => r.verdict.reason === "served tampered").length,
      )
      .toBe(1);
    await page.waitForLoadState("load");
    await new Promise((r) => setTimeout(r, SETTLE_MS));

    expect(await page.evaluate(() => window.__saTampered)).toBeUndefined();
    expect(await page.evaluate(() => typeof window.umami)).toBe("undefined");
    const gatewayCalls = (await recorded()).filter((r) => r.path === "/v1/c" || r.path === "/v1/e");
    expect(gatewayCalls).toEqual([]);

    // control: the untampered file runs on the same page, so the block above
    // came from the integrity check and not from a broken setup
    await setTampered(TRACKER_VERSION, false);
    await resetFixture();
    await page.reload();
    await trackerReady(page);
    await expectPageviews(site, 1);
  });
}

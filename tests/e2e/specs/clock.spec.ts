// a visitor whose system clock is two days ahead is still counted: the
// challenge time comes from the server and the client timestamp is never
// trusted (docs/reference/browser-proof.md)
import { expect, test } from "@playwright/test";
import { siteByKey } from "../sites.ts";
import { expectContract, expectPageviews, resetFixture, trackerReady } from "./helpers.ts";

const TWO_DAYS_MS = 2 * 24 * 60 * 60 * 1000;

test("a clock two days ahead is accepted", async ({ page }) => {
  await resetFixture();
  const site = siteByKey("static-html");
  const ahead = Date.now() + TWO_DAYS_MS;
  // moves Date.now and new Date() in the page without faking timers, as a
  // wrong system clock does
  await page.clock.setSystemTime(ahead);
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  const [pv] = await expectPageviews(site, 1);
  // the event carries the wrong clock, and the gateway still answers 202
  expect(pv?.ts).toBeGreaterThanOrEqual(ahead);
  expect((pv?.ts ?? 0) - Date.now()).toBeGreaterThan(TWO_DAYS_MS - 60_000);
  await expectContract(site);
});

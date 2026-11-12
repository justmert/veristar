// custom events from a data-umami-event attribute and from umami.track()
import { expect, test } from "@playwright/test";
import { SITES } from "../sites.ts";
import {
  expectContract,
  expectCustomEvents,
  expectPageviews,
  resetFixture,
  trackerReady,
} from "./helpers.ts";

test.beforeEach(async () => {
  await resetFixture();
});

for (const site of SITES) {
  test(`${site.key}: data-umami-event and umami.track() each send one event`, async ({ page }) => {
    await page.goto(`${site.origin}/`);
    await trackerReady(page);
    await expectPageviews(site, 1);

    await page.click("#signup");
    const first = await expectCustomEvents(site, 1);
    expect(first[0]?.n).toBe("signup-click");
    expect(first[0]?.d).toEqual({ plan: "free" });
    expect(first[0]?.u).toBe("/");

    await page.click("#manual");
    const both = await expectCustomEvents(site, 2);
    expect(both[1]?.n).toBe("manual-call");
    expect(both[1]?.d).toEqual({ source: site.key });

    // events of one page load share its challenge
    const pv = (await expectPageviews(site, 1))[0];
    expect(both.every((e) => e.p.salt === pv?.p.salt)).toBe(true);
    await expectContract(site);
  });
}

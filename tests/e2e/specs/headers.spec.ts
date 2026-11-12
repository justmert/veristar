// records the request headers each engine sends to the gateway, for the
// "observed request headers" section of docs/reference/browser-proof.md
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { SDK_PATH, siteByKey } from "../sites.ts";
import {
  expectContract,
  expectPageviews,
  recorded,
  resetFixture,
  trackerReady,
} from "./helpers.ts";

const KEEP = /^(sec-fetch-|sec-gpc$|origin$|referer$|content-type$|accept$|cookie$)/;

test("observed request headers", async ({ page, browserName, browser }, testInfo) => {
  await resetFixture();
  const site = siteByKey("static-html");
  await page.goto(`${site.origin}/`);
  await trackerReady(page);
  await expectPageviews(site, 1);
  await expectContract(site);

  const all = await recorded();
  const pick = (method: string, path: string) => {
    const r = all.find((x) => x.method === method && x.path === path);
    expect(r, `${method} ${path}`).toBeDefined();
    return Object.fromEntries(Object.entries(r?.headers ?? {}).filter(([k]) => KEEP.test(k)));
  };
  const observed = {
    engine: browserName,
    version: browser.version(),
    sdk: pick("GET", SDK_PATH),
    challenge: pick("GET", "/v1/c"),
    event: pick("POST", "/v1/e"),
  };
  const dir = join(testInfo.config.rootDir, "..", "test-results", "observed-headers");
  mkdirSync(dir, { recursive: true });
  writeFileSync(
    join(dir, `${testInfo.project.name}.json`),
    `${JSON.stringify(observed, null, 2)}\n`,
  );
  await testInfo.attach("observed-headers", {
    body: JSON.stringify(observed, null, 2),
    contentType: "application/json",
  });
});

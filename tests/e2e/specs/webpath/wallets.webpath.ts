// browser half of the this stage suite (tests/webpath wallets_test.go). the
// wallets kit example runs the real kit with the test wallet module, against
// the real gateway. each test drives one capture mode and checks what the
// browser sent; the go test checks the queue and what umami stored.
import { appendFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import {
  TEST_WALLET_ADDRESS,
  TEST_WALLET_ID,
  TEST_WALLET_NAME,
} from "../../../fixtures/wallets-kit-module/index.ts";
import { WALLET_SITE as site } from "../../sites.ts";
import {
  customEvents,
  expectContract,
  expectCustomEvents,
  expectPageviews,
  resetFixture,
  SETTLE_MS,
  trackerReady,
} from "../helpers.ts";

function env(name: string): string {
  const v = process.env[name];
  if (v === undefined || v === "") throw new Error(`missing env ${name}`);
  return v;
}

// every request the page makes, with its body, for the address scan
function recordRequests(page: Page, mode: string) {
  const file = join(env("WEBPATH_OUT"), "wallet-requests.jsonl");
  page.on("request", (r) => {
    appendFileSync(
      file,
      `${JSON.stringify({ mode, method: r.method(), url: r.url(), body: r.postData() ?? "" })}\n`,
    );
  });
}

async function names(): Promise<string[]> {
  return (await customEvents(site)).map((e) => e.n ?? "");
}

const MODES = [
  // sdk.js from the snippet with data-wallets-kit="true"
  { mode: "script", path: "/", network: undefined },
  // init() and attachWalletsKit(StellarWalletsKit) from the npm package
  { mode: "module", path: "/module.html", network: "testnet" },
] as const;

for (const { mode, path, network } of MODES) {
  test(`wallet events and custom events, ${mode} mode @wallets-${mode}`, async ({ page }) => {
    recordRequests(page, mode);
    const seed = env("WEBPATH_SEED");
    await resetFixture();
    await page.goto(`${site.origin}${path}`);
    await trackerReady(page);
    await expect(page.locator("#mode")).toHaveText(mode);
    await expectPageviews(site, 1);

    // connect through the kit's own modal, as a visitor would
    await page.click("#connect");
    await page.getByText(TEST_WALLET_NAME, { exact: true }).click();
    await expect(page.locator("#status")).toHaveText("connected");
    const connected = await expectCustomEvents(site, 2);
    // two sends in flight at once may arrive in either order
    expect(connected.map((e) => [e.n, e.d]).sort()).toEqual([
      ["wallet_connected", network ? { network } : undefined],
      ["wallet_selected", { wallet: TEST_WALLET_ID }],
    ]);
    await expectContract(site);

    // a reload restores the session from the kit's storage; the kit fires
    // its events again at launch, and none of them may count
    await resetFixture();
    await page.reload();
    await trackerReady(page);
    await expect(page.locator("#status")).toHaveText("connected");
    await expectPageviews(site, 1);
    await page.waitForTimeout(SETTLE_MS);
    expect(await names(), "no wallet event for a restored session").toEqual([]);

    await page.click("#disconnect");
    await expect(page.locator("#status")).toHaveText("not connected");
    const gone = await expectCustomEvents(site, 1);
    expect(gone.map((e) => [e.n, e.d])).toEqual([["wallet_disconnected", undefined]]);

    // custom events from an attribute and from a call
    await page.click("#signup");
    await expectCustomEvents(site, 2);
    await page.click("#manual");
    // a secret seed in event data is sent as the page wrote it; the gateway
    // redacts it before anything is queued
    await page.evaluate((s) => window.umami?.track("seed-check", { note: `key ${s}` }), seed);
    const custom = await expectCustomEvents(site, 4);
    expect(
      custom
        .slice(2)
        .map((e) => [e.n, e.d])
        .sort(),
    ).toEqual([
      ["manual-call", { source: "wallets-kit-spa" }],
      ["seed-check", { note: `key ${seed}` }],
    ]);
    expect([custom[1]?.n, custom[1]?.d]).toEqual(["signup-click", { plan: "free" }]);

    // the documented limits: the tracker refuses a 17th key and nested data
    // with a rejected promise and sends nothing
    const refused = await page.evaluate(async () => {
      const out: string[] = [];
      const many: Record<string, number> = {};
      for (let i = 1; i <= 17; i++) many[`k${i}`] = i;
      for (const [name, data] of [
        ["too-many-keys", many],
        ["nested-data", { outer: { inner: 1 } }],
      ] as const) {
        try {
          // biome-ignore lint/suspicious/noExplicitAny: nested data is the point of the check
          await window.umami?.track(name, data as any);
          out.push(`${name}: accepted`);
        } catch (e) {
          out.push(`${name}: ${(e as Error).message}`);
        }
      }
      return out;
    });
    expect(refused).toEqual([
      "too-many-keys: umami: event data has more than 16 keys",
      'nested-data: umami: event data value for "outer" must be a string, finite number or boolean',
    ]);
    await page.waitForTimeout(SETTLE_MS);
    expect(await names()).toHaveLength(4);
    await expectContract(site);
  });
}

// what a tenant's view-only member sees in umami: the custom and wallet
// events on the website's events page
test("wallet events in the umami dashboard @wallets-ui", async ({ page }) => {
  const base = env("WEBPATH_UMAMI_URL").replace(/\/$/, "");
  const website = env("WEBPATH_WEBSITE");
  await page.goto(
    `${base}/sso?token=${encodeURIComponent(env("WEBPATH_UMAMI_TOKEN"))}&url=${encodeURIComponent(`/websites/${website}/events`)}`,
  );
  await page.waitForURL(`**/websites/${website}/events`);
  for (const name of ["wallet_selected", "wallet_connected", "wallet_disconnected", "seed-check"]) {
    await expect(page.getByText(name, { exact: true }).first()).toBeVisible({ timeout: 30_000 });
  }
  await page.screenshot({ path: join(env("WEBPATH_OUT"), "ui-wallet-events.png"), fullPage: true });
  const text = await page.locator("body").innerText();
  writeFileSync(join(env("WEBPATH_OUT"), "ui-wallet-events.txt"), text);
  expect(text).not.toContain(TEST_WALLET_ADDRESS);
});

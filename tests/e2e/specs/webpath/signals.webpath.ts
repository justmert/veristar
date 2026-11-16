// page signals and counting rules of tracker 1.3.0 in chromium, firefox and
// webkit through the real gateway (tests/webpath TestPageSignals, which
// counts what the gateway reported). every test checks the browser side:
// which events the page sent and that the gateway answered each with 202.
//
// headless browsers show every page, so a background tab is emulated: an
// init script makes document.visibilityState report "hidden" until the test
// shows the page, and fires visibilitychange as the browser would. the
// tracker under test, the network, the gateway and the event loop are real.
// back/forward cache restores are real in chromium and firefox (the
// projects turn the cache on); playwright's webkit never keeps a page in
// that cache, so there the restore is webkit's own PageTransitionEvent
// dispatched on the live page. prerendering is real and chromium only: it
// runs in a chromium started without a debugger attached, because chromium
// turns prerendering off for pages under devtools.
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium, expect, test } from "@playwright/test";
import { PORTS, siteByKey } from "../../sites.ts";
import {
  eventsFor,
  expectContract,
  recorded,
  resetFixture,
  trackerReady,
  wire,
} from "../helpers.ts";

const staticSite = siteByKey("static-html");
const spa = siteByKey("vite-react-spa");

test.beforeEach(async () => {
  await resetFixture();
});

// the tracker's view of a background tab; window.__show() brings it forward
function hiddenUntilShown() {
  let state: "hidden" | "visible" = "hidden";
  Object.defineProperty(Document.prototype, "visibilityState", {
    get: () => state,
    configurable: true,
  });
  Object.defineProperty(Document.prototype, "hidden", {
    get: () => state === "hidden",
    configurable: true,
  });
  (window as unknown as { __show: () => void }).__show = () => {
    state = "visible";
    document.dispatchEvent(new Event("visibilitychange"));
  };
}

async function sent(site: typeof staticSite) {
  return eventsFor(await recorded(), site);
}

async function byType(site: typeof staticSite, t: string) {
  return (await sent(site)).map(wire).filter((e) => e.t === t);
}

async function expectCount(site: typeof staticSite, t: string, n: number) {
  await expect.poll(async () => (await byType(site, t)).length, { timeout: 15_000 }).toBe(n);
  await new Promise((r) => setTimeout(r, 1500));
  expect((await byType(site, t)).length, `no extra ${t} after settling`).toBe(n);
}

async function challengesFor(siteId: string) {
  return (await recorded()).filter((r) => r.path === "/v1/c" && r.query.w === siteId);
}

test("a background tab counts when it is first shown @signals", async ({ page }) => {
  await page.addInitScript(hiddenUntilShown);
  await page.goto(`${staticSite.origin}/`);
  await trackerReady(page);
  // the proof is fetched while hidden, so a notice could go out on close
  await expect.poll(async () => (await challengesFor(staticSite.siteId)).length).toBe(1);
  await new Promise((r) => setTimeout(r, 1500));
  expect(await byType(staticSite, "pv")).toHaveLength(0);
  await page.evaluate(() => (window as unknown as { __show: () => void }).__show());
  await expectCount(staticSite, "pv", 1);
  const [pv] = await byType(staticSite, "pv");
  // the smaller footprint: no title, screen or language, no frame flag
  expect(pv?.ti).toBeUndefined();
  expect(pv?.s).toBeUndefined();
  expect(pv?.l).toBeUndefined();
  expect(pv?.fr).toBeUndefined();
  expect(pv?.sv).toBe("1.3.0");
  await expectContract(staticSite);
});

test("a page left without being shown sends one never-visible notice @signals", async ({
  page,
}) => {
  await page.addInitScript(hiddenUntilShown);
  await page.goto(`${staticSite.origin}/`);
  await trackerReady(page);
  await expect.poll(async () => (await challengesFor(staticSite.siteId)).length).toBe(1);
  // time for the proof to be solved and kept
  await new Promise((r) => setTimeout(r, 2000));
  await page.goto("about:blank");
  await expectCount(staticSite, "nv", 1);
  expect(await byType(staticSite, "pv")).toHaveLength(0);
  const [nv] = await sent(staticSite);
  expect(nv?.verdict.status).toBe(202);
  expect(Object.keys(wire(nv as never)).sort()).toEqual(["h", "p", "sv", "t", "ts", "v", "w"]);
});

test("a restore from the back/forward cache counts again @signals", async ({
  page,
  browserName,
}) => {
  await page.goto(`${staticSite.origin}/`);
  await trackerReady(page);
  await expectCount(staticSite, "pv", 1);
  if (browserName === "webkit") {
    // see the file comment: webkit under playwright keeps no page in the cache
    await page.evaluate(() =>
      window.dispatchEvent(new PageTransitionEvent("pageshow", { persisted: true })),
    );
    await expectCount(staticSite, "pv", 2);
  } else {
    await page.click("#to-about");
    await page.waitForURL(`${staticSite.origin}/about.html`);
    await expectCount(staticSite, "pv", 2);
    expect(await challengesFor(staticSite.siteId)).toHaveLength(2);
    // history.back() from a timer: a cache restore fires no load event, which
    // page.goBack() would wait for
    await page.evaluate(() => setTimeout(() => history.back(), 0));
    await expectCount(staticSite, "pv", 3);
    // a restored page keeps its proof in memory, so it asks for no new
    // challenge; a page loaded again would have asked for one
    expect(
      await challengesFor(staticSite.siteId),
      "the home page came back from the back/forward cache",
    ).toHaveLength(2);
    expect((await byType(staticSite, "pv")).map((e) => e.u)).toEqual(["/", "/about.html", "/"]);
  }
  expect((await byType(staticSite, "nv")).length).toBe(0);
  await expectContract(staticSite);
});

test("history navigation in a single page app still counts @signals", async ({ page }) => {
  await page.goto(`${spa.origin}/`);
  await trackerReady(page);
  await expectCount(spa, "pv", 1);
  await page.click(spa.navSelector);
  await page.waitForURL(`${spa.origin}/pricing`);
  await expectCount(spa, "pv", 2);
  await page.goBack();
  await page.waitForURL(`${spa.origin}/`);
  await expectCount(spa, "pv", 3);
  expect((await byType(spa, "pv")).map((e) => e.u)).toEqual(["/", "/pricing", "/"]);
  await expectContract(spa);
});

test("a page inside a frame says so on every event @signals", async ({ page }) => {
  // an unrelated page that embeds the site; it never reaches the network
  const embedder = "https://embedder.sa-other.example/";
  await page.route(embedder, (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><title>embedder</title><iframe src="${spa.origin}/" width="1" height="1"></iframe>`,
    }),
  );
  await page.goto(embedder);
  const frame = page.frameLocator("iframe");
  await expect(frame.locator(spa.navSelector)).toBeVisible();
  await expectCount(spa, "pv", 1);
  const [pv] = await byType(spa, "pv");
  expect(pv?.fr).toBe(true);
  const evs = await sent(spa);
  expect(evs.every((r) => r.verdict.status === 202)).toBe(true);
  // the same site at top level carries no flag
  await resetFixture();
  await page.goto(`${spa.origin}/`);
  await expectCount(spa, "pv", 1);
  expect((await byType(spa, "pv"))[0]?.fr).toBeUndefined();
});

test("a prerendered page counts on activation and fetches nothing before @signals-prerender", async ({
  browserName,
}) => {
  test.skip(browserName !== "chromium", "prerendering exists in chromium only");
  const dir = mkdtempSync(join(tmpdir(), "sa-prerender-"));
  const proc = spawn(
    chromium.executablePath(),
    [
      "--headless",
      "--no-first-run",
      "--no-default-browser-check",
      `--user-data-dir=${dir}`,
      `--proxy-server=http://127.0.0.1:${PORTS.proxy}`,
      "--ignore-certificate-errors",
      `${spa.origin}/__prerender?to=/&delay=6000`,
    ],
    { stdio: "ignore" },
  );
  try {
    // the prerendered page loads sdk.js under the prerender purpose header
    await expect
      .poll(
        async () =>
          (await recorded()).some(
            (r) =>
              r.path.endsWith("/sdk.js") &&
              String(r.headers["sec-purpose"] ?? "").includes("prerender"),
          ),
        { timeout: 5000 },
      )
      .toBe(true);
    expect(await sent(spa), "nothing is sent while prerendered").toHaveLength(0);
    expect(await challengesFor(spa.siteId), "no challenge while prerendered").toHaveLength(0);
    // the launcher navigates after 6 s, which activates the page
    await expectCount(spa, "pv", 1);
    const evs = await sent(spa);
    expect(evs[0]?.verdict.status).toBe(202);
    expect(evs[0]?.headers["sec-purpose"]).toBeUndefined();
  } finally {
    // the profile is removed once chromium has exited, or it can still be
    // writing to it
    const exited = new Promise((resolve) => proc.once("exit", resolve));
    proc.kill();
    await exited;
    rmSync(dir, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
  }
});

// the two open questions about the redirect path of a handover, asked
// of every engine with the exact tenant snippet:
//   a. does a script loaded through a cross-origin 308 still pass its integrity check?
//   b. does a fetch post with keepalive follow a cross-origin 308 with its body?
// and c, the released tracker end to end with every gateway request redirected.
// each test prints one RESULT line with what the browser actually did, then
// asserts what a safe redirect path would need. a failing assertion is a finding.
import { expect, type Page, test } from "@playwright/test";
import type { SeenRequest } from "./fixture.ts";
import {
  NEW_HOST,
  OLD_BARE_HOST,
  OLD_HOST,
  REDIRECT_PORTS,
  TENANT_ORIGIN,
  TRACKER_VERSION,
  WRONG_VERSION,
} from "./hosts.ts";

const CONTROL = `http://127.0.0.1:${REDIRECT_PORTS.control}`;
const SDK_PATH = `/v/${TRACKER_VERSION}/sdk.js`;

async function reset(): Promise<void> {
  expect((await fetch(`${CONTROL}/reset`, { method: "POST" })).ok).toBe(true);
}

async function requests(): Promise<SeenRequest[]> {
  const res = await fetch(`${CONTROL}/requests`);
  expect(res.ok).toBe(true);
  return (await res.json()) as SeenRequest[];
}

function result(name: string, observation: Record<string, unknown>): void {
  console.log(`RESULT ${test.info().project.name} ${name} ${JSON.stringify(observation)}`);
}

function pageUrl(host: string, extra = ""): string {
  const q = new URLSearchParams({ origin: `https://${host}`, version: TRACKER_VERSION });
  return `${TENANT_ORIGIN}/page?${q.toString()}${extra}`;
}

// window.umami exists only when sdk.js ran; its version names the release
async function trackerVersion(page: Page): Promise<string | null> {
  await page.waitForLoadState("load");
  // the script is deferred and installs itself synchronously when it runs
  await page.waitForTimeout(500);
  return page.evaluate(
    () => (window as unknown as { umami?: { version: string } }).umami?.version ?? null,
  );
}

function consoleLines(page: Page): string[] {
  const lines: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error" || m.type() === "warning") lines.push(m.text());
  });
  return lines;
}

test.beforeAll(async ({ browser }) => {
  console.log(`ENGINE ${test.info().project.name} ${browser.version()}`);
});

test.beforeEach(async () => {
  await reset();
});

test.describe("a. integrity through a cross-origin 308", () => {
  test("control: direct load from the new origin runs", async ({ page }) => {
    const errors = consoleLines(page);
    await page.goto(pageUrl(NEW_HOST));
    const version = await trackerVersion(page);
    result("a-direct", { version, errors });
    expect(version).toBe(TRACKER_VERSION);
  });

  test("308 with access-control-allow-origin on the redirect runs", async ({ page }) => {
    const errors = consoleLines(page);
    await page.goto(pageUrl(OLD_HOST));
    const version = await trackerVersion(page);
    const all = await requests();
    const atOld = all.filter((r) => r.host === OLD_HOST && r.path === SDK_PATH).length;
    const atNew = all.filter((r) => r.host === NEW_HOST && r.path === SDK_PATH).length;
    result("a-308-cors", { version, sdkAtOld: atOld, sdkAtNew: atNew, errors });
    expect(atOld).toBe(1);
    expect(version).toBe(TRACKER_VERSION);
  });

  test("308 without any cors header on the redirect runs", async ({ page }) => {
    const errors = consoleLines(page);
    await page.goto(pageUrl(OLD_BARE_HOST));
    const version = await trackerVersion(page);
    const all = await requests();
    const atOld = all.filter((r) => r.host === OLD_BARE_HOST && r.path === SDK_PATH).length;
    const atNew = all.filter((r) => r.host === NEW_HOST && r.path === SDK_PATH).length;
    result("a-308-bare", { version, sdkAtOld: atOld, sdkAtNew: atNew, errors });
    expect(atOld).toBe(1);
    expect(version).toBe(TRACKER_VERSION);
  });

  test("control: a wrong integrity value through the 308 is blocked", async ({ page }) => {
    const errors = consoleLines(page);
    await page.goto(pageUrl(OLD_HOST, `&integrity=${WRONG_VERSION}`));
    const version = await trackerVersion(page);
    const atNew = (await requests()).filter(
      (r) => r.host === NEW_HOST && r.path === SDK_PATH,
    ).length;
    result("a-308-wrong-hash", { version, sdkAtNew: atNew, errors });
    // the bytes were fetched from the new origin, so the block is the integrity
    // check and not a failed request
    expect(atNew).toBe(1);
    expect(version).toBeNull();
  });
});

// a body the gateway would never accept, so nothing but the transport is tested
const BODY = JSON.stringify({ probe: "redirect-research", n: 42, s: "a".repeat(512) });

async function postFrom(page: Page, host: string) {
  await page.goto(`${TENANT_ORIGIN}/blank`);
  return page.evaluate(
    async ([url, body]) => {
      try {
        const res = await fetch(url as string, {
          method: "POST",
          keepalive: true,
          credentials: "omit",
          headers: { "Content-Type": "text/plain;charset=UTF-8" },
          body: body as string,
        });
        return {
          ok: true,
          status: res.status,
          type: res.type,
          redirected: res.redirected,
          url: res.url,
        };
      } catch (err) {
        return { ok: false, error: String(err) };
      }
    },
    [`https://${host}/v1/e`, BODY],
  );
}

async function arrivedAtNew(timeoutMs: number): Promise<SeenRequest | undefined> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const hit = (await requests()).find(
      (r) => r.host === NEW_HOST && r.method === "POST" && r.path === "/v1/e",
    );
    if (hit || Date.now() > deadline) return hit;
    await new Promise((r) => setTimeout(r, 200));
  }
}

test.describe("b. keepalive post through a cross-origin 308", () => {
  test("control: direct post to the new origin", async ({ page }) => {
    const page1 = await postFrom(page, NEW_HOST);
    const hit = await arrivedAtNew(5000);
    result("b-direct", {
      page: page1,
      arrived: !!hit,
      bodyIntact: hit?.body === BODY,
      origin: hit?.headers.origin,
    });
    expect(hit?.body).toBe(BODY);
  });

  test("308 with cors on the redirect keeps method and body", async ({ page }) => {
    const page1 = await postFrom(page, OLD_HOST);
    const hit = await arrivedAtNew(5000);
    const atOld = (await requests()).find((r) => r.host === OLD_HOST && r.method === "POST");
    result("b-308-cors", {
      page: page1,
      oldGotBody: atOld?.body === BODY,
      arrived: !!hit,
      method: hit?.method,
      bodyIntact: hit?.body === BODY,
      origin: hit?.headers.origin,
      secFetchSite: hit?.headers["sec-fetch-site"],
      referer: hit?.headers.referer,
    });
    expect(hit?.body).toBe(BODY);
  });

  test("308 without cors on the redirect keeps method and body", async ({ page }) => {
    const page1 = await postFrom(page, OLD_BARE_HOST);
    const hit = await arrivedAtNew(5000);
    result("b-308-bare", {
      page: page1,
      arrived: !!hit,
      bodyIntact: hit?.body === BODY,
      origin: hit?.headers.origin,
    });
    expect(hit?.body).toBe(BODY);
  });

  // the post starts in pagehide and the document is gone before it completes,
  // which is the case keepalive exists for (the tracker's last event of a page).
  // warm first sends one ordinary post the same way, as a real page has already
  // sent its pageview before it unloads, so the connections exist. headless
  // chromium behind the harness proxy drops an unload post that needs a new
  // connection even without any redirect, so the cold runs are recorded, not
  // asserted.
  async function postOnPagehide(page: Page, host: string, warm: boolean, name: string) {
    await page.goto(`${TENANT_ORIGIN}/blank`);
    await page.evaluate(
      async ([url, body, w]) => {
        const init = (b: string): RequestInit => ({
          method: "POST",
          keepalive: true,
          credentials: "omit",
          headers: { "Content-Type": "text/plain;charset=UTF-8" },
          body: b,
        });
        if (w === "warm") await fetch(url as string, init("warm")).catch(() => {});
        window.addEventListener("pagehide", () => {
          void fetch(url as string, init(body as string)).catch(() => {});
        });
      },
      [`https://${host}/v1/e`, BODY, warm ? "warm" : "cold"],
    );
    await page.goto(`${TENANT_ORIGIN}/blank?after=1`);
    const deadline = Date.now() + 8000;
    let hit: SeenRequest | undefined;
    let atFirst: SeenRequest | undefined;
    for (;;) {
      const all = await requests();
      hit = all.find((r) => r.host === NEW_HOST && r.method === "POST" && r.body === BODY);
      atFirst = all.find((r) => r.host === host && r.method === "POST" && r.body === BODY);
      if (hit || Date.now() > deadline) break;
      await new Promise((r) => setTimeout(r, 200));
    }
    result(name, {
      firstHostGotBody: !!atFirst,
      arrived: !!hit,
      bodyIntact: hit?.body === BODY,
      origin: hit?.headers.origin,
    });
    return hit;
  }

  test("control: direct post during pagehide", async ({ page }) => {
    const hit = await postOnPagehide(page, NEW_HOST, true, "b-direct-pagehide");
    expect(hit?.body).toBe(BODY);
  });

  test("308 with cors during pagehide", async ({ page }) => {
    const hit = await postOnPagehide(page, OLD_HOST, true, "b-308-cors-pagehide");
    expect(hit?.body).toBe(BODY);
  });

  test("observation: direct post during pagehide on a cold connection", async ({ page }) => {
    await postOnPagehide(page, NEW_HOST, false, "b-direct-pagehide-cold");
  });

  test("observation: 308 during pagehide on a cold connection", async ({ page }) => {
    await postOnPagehide(page, OLD_HOST, false, "b-308-cors-pagehide-cold");
  });
});

test.describe("c. released tracker with every gateway request redirected", () => {
  async function pageview(page: Page, host: string, name: string) {
    await page.goto(pageUrl(host));
    const version = await trackerVersion(page);
    const deadline = Date.now() + 8000;
    let all: SeenRequest[] = [];
    for (;;) {
      all = await requests();
      if (all.some((r) => r.host === NEW_HOST && r.path === "/v1/e") || Date.now() > deadline)
        break;
      await new Promise((r) => setTimeout(r, 200));
    }
    const challenge = all.filter((r) => r.host === NEW_HOST && r.path === "/v1/c");
    const events = all.filter((r) => r.host === NEW_HOST && r.path === "/v1/e");
    result(name, {
      version,
      challenge: challenge.map((r) => ({
        status: r.status,
        reason: r.reason,
        origin: r.headers.origin,
      })),
      events: events.map((r) => ({ status: r.status, reason: r.reason, origin: r.headers.origin })),
    });
    return events;
  }

  test("control: snippet on the new origin delivers a pageview", async ({ page }) => {
    const events = await pageview(page, NEW_HOST, "c-direct");
    expect(events.map((e) => e.status)).toEqual([202]);
  });

  test("old snippet through the 308 delivers an accepted pageview", async ({ page }) => {
    const events = await pageview(page, OLD_HOST, "c-308-cors");
    expect(events.map((e) => e.status)).toEqual([202]);
  });
});

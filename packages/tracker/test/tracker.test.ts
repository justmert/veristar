import { JSDOM } from "jsdom";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createTracker, type Tracker, type TrackerOptions, VERSION } from "../src/index.ts";
import { verifyNonce } from "../src/proof.ts";
import type { WireEvent } from "../src/wire.ts";
import {
  type Gateway,
  pause,
  type Recorded,
  startGateway,
  tagFor,
  waitFor,
} from "./gateway-fixture.ts";

const SITE = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b";
// long enough for the 300 ms navigation delay plus a proof at fixture difficulty
const QUIET_MS = 600;

type PageOptions = {
  url?: string;
  referrer?: string;
  html?: string;
  gpc?: boolean;
  dnt?: string;
  optOut?: boolean;
};

let gw: Gateway;
let windows: (Window & typeof globalThis)[] = [];

beforeEach(async () => {
  gw = await startGateway(8);
});

afterEach(async () => {
  for (const w of windows) w.close();
  windows = [];
  await gw.close();
});

// gives a jsdom window the real fetch and webcrypto of node, which jsdom lacks
function browserize(window: Window & typeof globalThis, page: PageOptions) {
  window.fetch = fetch;
  Object.defineProperty(window, "crypto", { value: globalThis.crypto, configurable: true });
  if (page.gpc) Object.defineProperty(window.navigator, "globalPrivacyControl", { value: true });
  if (page.dnt) Object.defineProperty(window.navigator, "doNotTrack", { value: page.dnt });
}

function openPage(page: PageOptions = {}): Window & typeof globalThis {
  const dom = new JSDOM(page.html ?? "<!doctype html><title>Home</title><body></body>", {
    url: page.url ?? "https://site.example/start?q=1#top",
    referrer: page.referrer,
    beforeParse: (window) => browserize(window, page),
  });
  if (page.optOut) dom.window.localStorage.setItem("umami.disabled", "1");
  windows.push(dom.window);
  return dom.window;
}

function track(window: Window, options: Partial<TrackerOptions> = {}): Tracker {
  return createTracker(window, { siteId: SITE, host: gw.origin, ...options });
}

function parse(r: Recorded): WireEvent {
  return JSON.parse(r.body) as WireEvent;
}

async function expectValidProof(e: WireEvent) {
  expect(e.p.tag).toBe(tagFor(e.w, e.p.iat, e.p.salt, e.p.bits));
  expect(await verifyNonce(crypto.subtle, e.w, e.p)).toBe(true);
}

describe("pageviews", () => {
  it("sends one wire v1 pageview with a valid proof on load", async () => {
    const w = openPage({ referrer: "https://ref.example/from?x=1" });
    track(w);
    await waitFor(() => gw.events().length === 1);
    await pause(QUIET_MS);
    expect(gw.events()).toHaveLength(1);

    const req = gw.events()[0] as Recorded;
    expect(req.method).toBe("POST");
    expect(req.headers["content-type"]).toBe("text/plain;charset=UTF-8");
    const e = parse(req);
    expect(Object.keys(e).sort()).toEqual([
      "h",
      "l",
      "p",
      "r",
      "s",
      "sv",
      "t",
      "ti",
      "ts",
      "u",
      "v",
      "w",
    ]);
    expect(e).toMatchObject({
      v: 1,
      w: SITE,
      t: "pv",
      h: "site.example",
      // "#top" is not a route fragment, so it is not sent
      u: "/start?q=1",
      r: "https://ref.example/from?x=1",
      ti: "Home",
      l: "en-US",
    });
    expect(e.s).toMatch(/^\d+x\d+$/);
    expect(Math.abs(e.ts - Date.now())).toBeLessThan(10000);
    expect(Object.keys(e.p).sort()).toEqual(["bits", "iat", "nonce", "salt", "tag"]);
    await expectValidProof(e);

    const challenge = gw.challenges()[0] as Recorded;
    expect(challenge.method).toBe("GET");
    expect(challenge.path).toBe(`/v1/c?w=${SITE}`);
  });

  it("keeps a same-origin referrer as a path", async () => {
    const w = openPage({ referrer: "https://site.example/previous?a=1" });
    track(w);
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).r).toBe("/previous?a=1");
  });

  it("excludes search and hash when asked", async () => {
    const w = openPage();
    track(w, { excludeSearch: true, excludeHash: true });
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).u).toBe("/start");
  });

  it("fetches one challenge per page load for many events", async () => {
    const w = openPage();
    const t = track(w);
    await Promise.all([t.track("a"), t.track("b"), t.track("c")]);
    await waitFor(() => gw.events().length === 4);
    expect(gw.challenges()).toHaveLength(1);
    const nonces = new Set(gw.events().map((r) => parse(r).p.nonce));
    expect(nonces.size).toBe(1);
  });
});

describe("spa navigation", () => {
  it("counts pushState, ignores replaceState to the same url, counts back", async () => {
    const w = openPage({ url: "https://site.example/" });
    track(w);
    await waitFor(() => gw.events().length === 1);

    w.history.pushState({}, "", "/next");
    await waitFor(() => gw.events().length === 2);
    const second = parse(gw.events()[1] as Recorded);
    expect(second.u).toBe("/next");
    expect(second.r).toBe("/");

    w.history.replaceState({}, "", "/next");
    await pause(QUIET_MS);
    expect(gw.events()).toHaveLength(2);

    w.history.back();
    await waitFor(() => gw.events().length === 3);
    const third = parse(gw.events()[2] as Recorded);
    expect(third.u).toBe("/");
    expect(third.r).toBe("/next");
  });
});

describe("long spa sessions", () => {
  it("keeps every pageview of 60 navigations under the gateway's 50 events per proof", async () => {
    gw.replayLimit = 50;
    const w = openPage({ url: "https://site.example/" });
    track(w);
    await waitFor(() => gw.events().length === 1);
    for (let i = 0; i < 60; i++) {
      w.history.pushState({}, "", `/page/${i}`);
      await waitFor(() => gw.events().length === i + 2);
    }
    const statuses = gw.events().map((r) => r.status);
    expect(statuses).toHaveLength(61);
    expect(statuses.every((s) => s === 202)).toBe(true);
    // a new challenge after 40 uses, before the limit
    expect(gw.challenges()).toHaveLength(2);
    expect(parse(gw.events()[60] as Recorded).u).toBe("/page/59");
  });

  it("fetches a new challenge after a replay refusal and sends the event once more", async () => {
    // a gateway configured below the tracker's own budget
    gw.replayLimit = 5;
    const w = openPage();
    const t = track(w, { autoTrack: false });
    for (let i = 0; i < 12; i++) await t.track(`e${i}`);
    const accepted = gw.events().filter((r) => r.status === 202);
    const refused = gw.events().filter((r) => r.status === 403);
    expect(accepted.map((r) => parse(r).n)).toEqual(Array.from({ length: 12 }, (_, i) => `e${i}`));
    expect(refused.map((r) => parse(r).n)).toEqual(["e5", "e10"]);
    expect(gw.challenges()).toHaveLength(3);
  });

  it("sends a refused event at most twice", async () => {
    gw.replayLimit = 0;
    const w = openPage();
    const t = track(w, { autoTrack: false });
    await t.track("once");
    // the retry with a fresh proof is refused too, and then the tracker stops
    expect(gw.events().map((r) => r.status)).toEqual([403, 403]);
    expect(gw.challenges()).toHaveLength(2);
  });

  it("names the release on every event", async () => {
    const w = openPage();
    track(w);
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).sv).toBe(VERSION);
  });
});

describe("back and forward in next.js", () => {
  it("keeps the previous page as referrer when the router replaces the current url", async () => {
    const w = openPage({ url: "https://site.example/a" });
    track(w);
    await waitFor(() => gw.events().length === 1);
    w.history.pushState({}, "", "/b");
    await waitFor(() => gw.events().length === 2);
    // next.js answers popstate with replaceState for the url it restored
    w.addEventListener("popstate", () =>
      w.history.replaceState({ __NA: true }, "", w.location.href),
    );
    w.history.back();
    await waitFor(() => gw.events().length === 3);
    const back = parse(gw.events()[2] as Recorded);
    expect(back.u).toBe("/a");
    expect(back.r).toBe("/b");
    w.history.forward();
    await waitFor(() => gw.events().length === 4);
    const forward = parse(gw.events()[3] as Recorded);
    expect(forward.u).toBe("/b");
    expect(forward.r).toBe("/a");
    await pause(QUIET_MS);
    expect(gw.events()).toHaveLength(4);
  });
});

describe("url fragments", () => {
  const cases: [string, string, boolean, string][] = [
    ["sends a route fragment", "https://site.example/app#/users/7", false, "/app#/users/7"],
    [
      "drops the query inside a route fragment",
      "https://site.example/app?x=1#/a?b=c",
      false,
      "/app?x=1#/a",
    ],
    [
      "drops an oauth token fragment",
      "https://site.example/cb#access_token=s3cret&type=bearer",
      false,
      "/cb",
    ],
    ["drops an anchor fragment", "https://site.example/doc#section-2", false, "/doc"],
    ["drops a bare #", "https://site.example/doc#", false, "/doc"],
    [
      "drops a route fragment with data-exclude-hash",
      "https://site.example/app#/users/7",
      true,
      "/app",
    ],
  ];
  for (const [name, url, excludeHash, expected] of cases) {
    it(name, async () => {
      const w = openPage({ url });
      track(w, { excludeHash });
      await waitFor(() => gw.events().length === 1);
      expect(parse(gw.events()[0] as Recorded).u).toBe(expected);
    });
  }

  it("applies the rule to a url set by before-send", async () => {
    const w = openPage();
    track(w, { beforeSend: (_t, p) => ({ ...p, url: "/x#id_token=abc" }) });
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).u).toBe("/x");
  });

  it("counts hash route changes and ignores anchor jumps", async () => {
    const w = openPage({ url: "https://site.example/app#/a" });
    track(w);
    await waitFor(() => gw.events().length === 1);
    w.history.pushState({}, "", "#/b");
    await waitFor(() => gw.events().length === 2);
    expect(parse(gw.events()[1] as Recorded)).toMatchObject({ u: "/app#/b", r: "/app#/a" });
    w.location.hash = "#/b?tab=2";
    await pause(QUIET_MS);
    expect(gw.events()).toHaveLength(2);
    w.location.hash = "#footnote";
    await waitFor(() => gw.events().length === 3);
    expect(parse(gw.events()[2] as Recorded).u).toBe("/app");
    w.location.hash = "#other-anchor";
    await pause(QUIET_MS);
    expect(gw.events()).toHaveLength(3);
  });
});

describe("custom events", () => {
  it("sends umami.track(name, data) as an ev event", async () => {
    const w = openPage();
    const t = track(w, { autoTrack: false });
    await t.track("signup", { plan: "pro", seats: 3, trial: true });
    await waitFor(() => gw.events().length === 1);
    const e = parse(gw.events()[0] as Recorded);
    expect(e.t).toBe("ev");
    expect(e.n).toBe("signup");
    expect(e.d).toEqual({ plan: "pro", seats: 3, trial: true });
    await expectValidProof(e);
  });

  it("captures clicks on data-umami-event elements", async () => {
    const w = openPage({
      html: '<!doctype html><title>t</title><button data-umami-event="cta" data-umami-event-place="hero">go</button>',
    });
    track(w);
    await waitFor(() => gw.events().length === 1);
    (w.document.querySelector("button") as HTMLButtonElement).click();
    await waitFor(() => gw.events().length === 2);
    const e = parse(gw.events()[1] as Recorded);
    expect(e).toMatchObject({ t: "ev", n: "cta", d: { place: "hero" } });
  });

  it("truncates long event names to 50 characters", async () => {
    const w = openPage();
    const t = track(w, { autoTrack: false });
    await t.track("n".repeat(80));
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).n).toBe("n".repeat(50));
  });

  it("rejects nested event data and oversized bodies without sending", async () => {
    const w = openPage();
    const t = track(w, { autoTrack: false });
    await expect(t.track("x", { a: { b: 1 } } as never)).rejects.toThrow(/event data value/);
    await expect(t.track("x", { a: null } as never)).rejects.toThrow(/event data value/);
    await expect(t.track("x", { a: "nul\u0000" })).rejects.toThrow(/nul character/);
    await expect(t.track("x", { "k\n": 1 })).rejects.toThrow(/control characters/);
    await expect(t.track("line\nbreak")).rejects.toThrow(/control characters/);
    // values umami cannot store
    await expect(t.track("swap", { amount: 1e15 })).rejects.toThrow(/below 1e15/);
    await expect(t.track("x", { at: "0000-01-01T00:00:00Z" })).rejects.toThrow(/before year 1/);
    await expect(t.track("x", { revenue: 1e14, currency: "USD" })).rejects.toThrow(/revenue/);
    await expect(t.track("-x")).rejects.toThrow(/must not start/);
    const big = Object.fromEntries(
      Array.from({ length: 16 }, (_, i) => [`key${i}`, "v".repeat(500)]),
    );
    await expect(t.track("x", big)).rejects.toThrow(/exceeds 8192 bytes/);
    await pause(QUIET_MS);
    expect(gw.events()).toHaveLength(0);
  });
});

describe("before-send", () => {
  it("can modify the payload", async () => {
    const w = openPage();
    track(w, { beforeSend: (_type, p) => ({ ...p, url: "/rewritten?k=v", title: "T2" }) });
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded)).toMatchObject({ u: "/rewritten?k=v", ti: "T2" });
  });

  it("can drop the event, and then no challenge is fetched", async () => {
    const w = openPage();
    const seen: string[] = [];
    track(w, {
      beforeSend: (type) => {
        seen.push(type);
        return null;
      },
    });
    await waitFor(() => seen.length === 1);
    await pause(QUIET_MS);
    expect(seen).toEqual(["event"]);
    expect(gw.requests).toHaveLength(0);
  });

  it("can be async", async () => {
    const w = openPage();
    track(w, { beforeSend: async (_t, p) => ({ ...p, title: "async" }) });
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).ti).toBe("async");
  });
});

describe("nothing is sent", () => {
  const cases: [string, PageOptions, Partial<TrackerOptions>][] = [
    ["with global privacy control", { gpc: true }, {}],
    ["with the umami.disabled opt-out flag", { optOut: true }, {}],
    ["on a hostname outside data-domains", {}, { domains: ["other.example"] }],
    ["with do-not-track when the site honours it", { dnt: "1" }, { doNotTrack: true }],
  ];
  for (const [name, page, options] of cases) {
    it(name, async () => {
      const w = openPage(page);
      const t = track(w, options);
      await pause(QUIET_MS);
      await t.track("manual");
      await pause(100);
      expect(gw.requests).toHaveLength(0);
    });
  }

  it("still sends with do-not-track when the site does not honour it", async () => {
    const w = openPage({ dnt: "1" });
    track(w);
    await waitFor(() => gw.events().length === 1);
  });

  it("sends on a listed domain", async () => {
    const w = openPage();
    track(w, { domains: ["other.example", " site.example "] });
    await waitFor(() => gw.events().length === 1);
  });
});

describe("deferred start", () => {
  it("sends nothing until track() or start() is called", async () => {
    const w = openPage({ url: "https://site.example/" });
    const t = track(w, { autoTrack: false });
    await pause(QUIET_MS);
    expect(gw.requests).toHaveLength(0);

    t.start();
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).t).toBe("pv");

    // start() also installs the history hooks
    w.history.pushState({}, "", "/after-consent");
    await waitFor(() => gw.events().length === 2);
    expect(parse(gw.events()[1] as Recorded).u).toBe("/after-consent");

    t.start();
    await pause(QUIET_MS);
    expect(gw.events()).toHaveLength(2);
  });
});

describe("gateway failures", () => {
  it("asks for a new challenge after a failed one", async () => {
    gw.bits = 99;
    const w = openPage();
    const t = track(w, { autoTrack: false });
    await t.track("first");
    expect(gw.events()).toHaveLength(0);
    gw.bits = 8;
    await t.track("second");
    await waitFor(() => gw.events().length === 1);
    expect(gw.challenges()).toHaveLength(2);
    expect(parse(gw.events()[0] as Recorded).n).toBe("second");
  });
});

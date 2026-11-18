// runs the released sdk.js bundle (not the source) in jsdom, loaded from a
// script tag, so the attribute handling of src/script.ts is covered too
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { JSDOM } from "jsdom";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { WireEvent } from "../src/wire.ts";
import { type Gateway, pause, type Recorded, startGateway, waitFor } from "./gateway-fixture.ts";

const SITE = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b";
const sdk = (version: string) =>
  readFileSync(join(import.meta.dirname, "..", "releases", version, "sdk.js"));

let gw: Gateway;
let other: Gateway;
let windows: (Window & typeof globalThis)[] = [];

beforeEach(async () => {
  gw = await startGateway(8);
  other = await startGateway(8);
  gw.files.set("/v/1.0.0/sdk.js", { type: "text/javascript", body: sdk("1.0.0") });
});

afterEach(async () => {
  for (const w of windows) w.close();
  windows = [];
  await gw.close();
  await other.close();
});

function load(attrs: string, extraHead = "", version = "1.0.0"): Window & typeof globalThis {
  const html = `<!doctype html><html><head><title>Page</title>${extraHead}<script defer src="${gw.origin}/v/${version}/sdk.js" data-website-id="${SITE}" ${attrs}></script></head><body><a id="l" href="#">x</a></body></html>`;
  const dom = new JSDOM(html, {
    url: "https://site.example/p?q=1#h",
    runScripts: "dangerously",
    resources: "usable",
    // from 1.3.0 a page counts only once it is visible, which jsdom reports
    // only when it pretends to render
    pretendToBeVisual: version !== "1.0.0",
    beforeParse: (window) => {
      window.fetch = fetch;
      Object.defineProperty(window, "crypto", { value: globalThis.crypto, configurable: true });
    },
  });
  windows.push(dom.window);
  return dom.window;
}

const parse = (r: Recorded) => JSON.parse(r.body) as WireEvent;

describe("released sdk.js 1.0.0", () => {
  it("sends a pageview to the origin it was loaded from and exposes umami", async () => {
    const w = load("");
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded)).toMatchObject({
      v: 1,
      w: SITE,
      t: "pv",
      // "#h" is not a route fragment, so it is not sent
      u: "/p?q=1",
    });
    expect(w.umami?.version).toBe("1.0.0");
    await w.umami?.track("from-api", { k: "v" });
    await waitFor(() => gw.events().length === 2);
    expect(parse(gw.events()[1] as Recorded)).toMatchObject({
      t: "ev",
      n: "from-api",
      d: { k: "v" },
    });
  });

  it("honours data-host-url, data-exclude-search and data-exclude-hash", async () => {
    load(`data-host-url="${other.origin}" data-exclude-search="true" data-exclude-hash="true"`);
    await waitFor(() => other.events().length === 1);
    expect(parse(other.events()[0] as Recorded).u).toBe("/p");
    expect(gw.events()).toHaveLength(0);
  });

  it("calls the data-before-send hook by name", async () => {
    load(
      'data-before-send="hook"',
      '<script>window.hook = (type, p) => ({ ...p, title: type + ":hooked" });</script>',
    );
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded).ti).toBe("event:hooked");
  });

  it("sends nothing with data-auto-track=false until umami.track()", async () => {
    const w = load('data-auto-track="false"');
    await waitFor(() => w.umami !== undefined);
    await pause(600);
    expect(gw.requests.filter((r) => r.path !== "/v/1.0.0/sdk.js")).toHaveLength(0);
    await w.umami?.track();
    await waitFor(() => gw.events().length === 1);
  });

  it("sends nothing outside data-domains", async () => {
    const w = load('data-domains="elsewhere.example"');
    await waitFor(() => w.umami !== undefined);
    await w.umami?.track("x");
    await pause(600);
    expect(gw.events()).toHaveLength(0);
    expect(gw.challenges()).toHaveLength(0);
  });
});

describe("released sdk.js 1.4.0", () => {
  beforeEach(() => {
    gw.files.set("/v/1.4.0/sdk.js", { type: "text/javascript", body: sdk("1.4.0") });
  });

  it("installs one tracker as both window.veristar and window.umami", async () => {
    const w = load('data-auto-track="false"', "", "1.4.0");
    await waitFor(() => w.veristar !== undefined);
    expect(w.veristar).toBe(w.umami);
    expect(w.veristar?.version).toBe("1.4.0");
    expect(typeof w.veristar?.track).toBe("function");
    expect(typeof w.veristar?.start).toBe("function");
    await pause(600);
    expect(gw.events()).toHaveLength(0);
    w.veristar?.start();
    await waitFor(() => gw.events().length === 1);
    expect(parse(gw.events()[0] as Recorded)).toMatchObject({ t: "pv", u: "/p?q=1" });
    await w.veristar?.track("from-veristar", { k: "v" });
    await w.umami?.track("from-umami");
    await waitFor(() => gw.events().length === 3);
    expect(gw.events().map((r) => parse(r).n)).toEqual([undefined, "from-veristar", "from-umami"]);
  });

  it("leaves a tracker already installed under either name alone", async () => {
    const w = load("", "<script>window.veristar = { version: 'mine' };</script>", "1.4.0");
    await pause(600);
    expect(w.veristar?.version).toBe("mine");
    expect(w.umami).toBeUndefined();
    expect(gw.events()).toHaveLength(0);
  });
});

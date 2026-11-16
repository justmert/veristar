// @vitest-environment jsdom
// the wallets kit adapter against the real kit at both pinned versions
// (2.7.1 and 2.8.0). the kit runs its own code: init,
// setWallet, fetchAddress, setNetwork, disconnect, its signals, its
// localStorage writes. the only stand-in is the wallet itself, the test
// module from tests/fixtures/wallets-kit-module, because a test runner has
// no browser extension. events go through the real tracker to a local
// gateway fixture, so assertions are on the bytes put on the network.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { JSDOM } from "jsdom";
import * as kit271 from "swk-2.7.1";
import * as kit280 from "swk-2.8.0";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import {
  TEST_WALLET_ADDRESS,
  TEST_WALLET_ID,
  TestWalletModule,
} from "../../../tests/fixtures/wallets-kit-module/index.ts";
import { createTracker, type Tracker } from "../src/index.ts";
import {
  attachWalletsKit,
  KIT_ADDRESS_KEY,
  KIT_SELECTED_KEY,
  networkName,
  STORAGE_POLL_MS,
  WALLET_CONNECTED,
  WALLET_DISCONNECTED,
  WALLET_SELECTED,
  watchWalletsKitStorage,
} from "../src/wallets-kit.ts";
import type { WireEvent } from "../src/wire.ts";
import { type Gateway, pause, startGateway, waitFor } from "./gateway-fixture.ts";

const SITE = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b";
// time for the tracker to solve its proof and post at fixture difficulty
const QUIET_MS = 400;

type KitModule = typeof kit280;
const KITS: [string, KitModule][] = [
  ["2.7.1", kit271 as unknown as KitModule],
  ["2.8.0", kit280],
];

let gw: Gateway;
beforeAll(async () => {
  gw = await startGateway(4);
});
afterAll(async () => {
  await gw.close();
});

function walletEvents(): { n: string | undefined; d: WireEvent["d"] }[] {
  return gw
    .events()
    .map((r) => JSON.parse(r.body) as WireEvent)
    .filter((e) => e.t === "ev")
    .map((e) => ({ n: e.n, d: e.d }));
}

function expectNoAddressOnTheWire() {
  for (const r of gw.requests) {
    expect(r.body).not.toContain(TEST_WALLET_ADDRESS);
    expect(r.path).not.toContain(TEST_WALLET_ADDRESS);
  }
}

function newTracker(): Tracker {
  return createTracker(window, { siteId: SITE, host: gw.origin, autoTrack: false });
}

describe.each(KITS)("stellar wallets kit %s", (version, mod) => {
  const { StellarWalletsKit, Networks, KitEventType } = mod;
  let wallet: TestWalletModule;

  beforeAll(() => {
    wallet = new TestWalletModule();
    // the kit's ModuleInterface; the fixture implements it without importing the kit
    StellarWalletsKit.init({
      modules: [wallet as unknown as Parameters<typeof StellarWalletsKit.init>[0]["modules"][0]],
      network: Networks.TESTNET,
    });
  });

  beforeEach(async () => {
    // every test starts with no wallet, no address and an empty recorder
    await StellarWalletsKit.disconnect();
    StellarWalletsKit.setNetwork(Networks.TESTNET);
    localStorage.clear();
    await pause(QUIET_MS);
    gw.requests.length = 0;
  });

  afterEach(() => {
    expectNoAddressOnTheWire();
  });

  it("is the pinned kit version", () => {
    const pkg = join(import.meta.dirname, "..", "node_modules", `swk-${version}`, "package.json");
    expect(JSON.parse(readFileSync(pkg, "utf8")).version).toBe(version);
  });

  it("uses the event names and storage keys the adapter expects", () => {
    expect(KitEventType.STATE_UPDATED).toBe("STATE_UPDATE");
    expect(KitEventType.WALLET_SELECTED).toBe("WALLET_SELECTED");
    expect(KitEventType.DISCONNECT).toBe("DISCONNECT");
    expect(mod.LocalStorageKeys.selectedModuleId).toBe(KIT_SELECTED_KEY);
    expect(mod.LocalStorageKeys.activeAddress).toBe(KIT_ADDRESS_KEY);
    // the kit sets no browser global a separately loaded script could use
    expect(Object.keys(window).filter((k) => /wallet|swk/i.test(k))).toEqual([]);
  });

  it("each kit event yields the right tracker event once", async () => {
    const tracker = newTracker();
    const detach = attachWalletsKit(StellarWalletsKit, { tracker });

    StellarWalletsKit.setWallet(TEST_WALLET_ID);
    await StellarWalletsKit.fetchAddress();
    // fetching the address again, and a network change while connected, are
    // no new connection
    await StellarWalletsKit.fetchAddress();
    StellarWalletsKit.setNetwork(Networks.PUBLIC);
    StellarWalletsKit.setNetwork(Networks.TESTNET);
    await StellarWalletsKit.disconnect();

    await waitFor(() => walletEvents().length === 3);
    await pause(QUIET_MS);
    expect(walletEvents()).toEqual([
      { n: WALLET_SELECTED, d: { wallet: TEST_WALLET_ID } },
      { n: WALLET_CONNECTED, d: { network: "testnet" } },
      { n: WALLET_DISCONNECTED, d: undefined },
    ]);
    expect(wallet.addressRequests).toBeGreaterThanOrEqual(2);
    detach();
  });

  it("names the network from the passphrase at connection time", async () => {
    const detach = attachWalletsKit(StellarWalletsKit, { tracker: newTracker() });
    for (const network of [Networks.PUBLIC, Networks.FUTURENET, Networks.STANDALONE]) {
      StellarWalletsKit.setNetwork(network);
      StellarWalletsKit.setWallet(TEST_WALLET_ID);
      await StellarWalletsKit.fetchAddress();
      await StellarWalletsKit.disconnect();
    }
    await waitFor(() => walletEvents().length === 9);
    await pause(QUIET_MS);
    expect(walletEvents().filter((e) => e.n === WALLET_CONNECTED)).toEqual([
      { n: WALLET_CONNECTED, d: { network: "public" } },
      { n: WALLET_CONNECTED, d: { network: "futurenet" } },
      { n: WALLET_CONNECTED, d: { network: "other" } },
    ]);
    detach();
  });

  it("does not count the launch-time firings of a restored session", async () => {
    StellarWalletsKit.setWallet(TEST_WALLET_ID);
    await StellarWalletsKit.fetchAddress();
    // the kit now holds a connected session, as after a page load that
    // restored it from localStorage. attaching makes the kit fire
    // STATE_UPDATE with the address and WALLET_SELECTED with the id at once.
    const seen: string[] = [];
    const probe = [
      StellarWalletsKit.on(KitEventType.STATE_UPDATED, (e) => seen.push(e.eventType)),
      StellarWalletsKit.on(KitEventType.WALLET_SELECTED, (e) => seen.push(e.eventType)),
    ];
    for (const stop of probe) stop();
    expect(seen).toEqual(["STATE_UPDATE", "WALLET_SELECTED"]);

    const detach = attachWalletsKit(StellarWalletsKit, { tracker: newTracker() });
    await pause(QUIET_MS);
    expect(walletEvents()).toEqual([]);
    // the session goes on normally afterwards
    await StellarWalletsKit.disconnect();
    await waitFor(() => walletEvents().length === 1);
    expect(walletEvents()).toEqual([{ n: WALLET_DISCONNECTED, d: undefined }]);
    detach();
  });

  it("detach stops capture and a kit cannot be attached twice", async () => {
    const tracker = newTracker();
    const detach = attachWalletsKit(StellarWalletsKit, { tracker });
    expect(() => attachWalletsKit(StellarWalletsKit, { tracker })).toThrow(/already attached/);
    detach();
    detach();
    StellarWalletsKit.setWallet(TEST_WALLET_ID);
    await StellarWalletsKit.fetchAddress();
    await StellarWalletsKit.disconnect();
    await pause(QUIET_MS);
    expect(walletEvents()).toEqual([]);
    // attaching again after detach is allowed
    attachWalletsKit(StellarWalletsKit, { tracker })();
  });

  it("the storage watcher sends the same events without reading the address", async () => {
    const reads: string[] = [];
    const getItem = Storage.prototype.getItem;
    Storage.prototype.getItem = function (this: Storage, key: string) {
      reads.push(key);
      return getItem.call(this, key);
    };
    const tracker = newTracker();
    const stop = watchWalletsKitStorage(window, tracker.track);
    try {
      StellarWalletsKit.setWallet(TEST_WALLET_ID);
      await StellarWalletsKit.fetchAddress();
      await waitFor(() => walletEvents().length === 2);
      await StellarWalletsKit.disconnect();
      await waitFor(() => walletEvents().length === 3);
      await pause(STORAGE_POLL_MS + QUIET_MS);
    } finally {
      stop();
      Storage.prototype.getItem = getItem;
    }
    expect(walletEvents()).toEqual([
      { n: WALLET_SELECTED, d: { wallet: TEST_WALLET_ID } },
      // the kit keeps the network in memory only, so storage cannot tell it
      { n: WALLET_CONNECTED, d: undefined },
      { n: WALLET_DISCONNECTED, d: undefined },
    ]);
    // the kit reads its address key only when it is imported, so every read
    // in this window would be the watcher's, and there is none
    expect(reads.filter((k) => k === KIT_ADDRESS_KEY)).toEqual([]);
    expect(reads.filter((k) => k === KIT_SELECTED_KEY).length).toBeGreaterThan(0);
  });
});

describe("storage watcher", () => {
  let stop: () => void = () => undefined;
  const sent: { name: string; data?: unknown }[] = [];
  const track = async (name: string, data?: unknown) => {
    sent.push({ name, data });
  };

  beforeEach(() => {
    localStorage.clear();
    sent.length = 0;
  });
  afterEach(() => stop());

  it("treats the state at start as the restored session", async () => {
    localStorage.setItem(KIT_SELECTED_KEY, "xbull");
    localStorage.setItem(KIT_ADDRESS_KEY, "present");
    stop = watchWalletsKitStorage(window, track);
    await pause(STORAGE_POLL_MS * 2);
    expect(sent).toEqual([]);
    localStorage.removeItem(KIT_ADDRESS_KEY);
    localStorage.removeItem(KIT_SELECTED_KEY);
    await pause(STORAGE_POLL_MS * 2);
    expect(sent).toEqual([{ name: WALLET_DISCONNECTED, data: undefined }]);
  });

  it("leaves changes made by another tab to that tab", async () => {
    stop = watchWalletsKitStorage(window, track);
    localStorage.setItem(KIT_SELECTED_KEY, "xbull");
    localStorage.setItem(KIT_ADDRESS_KEY, "present");
    window.dispatchEvent(new StorageEvent("storage", { key: KIT_ADDRESS_KEY }));
    await pause(STORAGE_POLL_MS * 2);
    expect(sent).toEqual([]);
  });

  it("stops when asked", async () => {
    stop = watchWalletsKitStorage(window, track);
    stop();
    localStorage.setItem(KIT_SELECTED_KEY, "xbull");
    await pause(STORAGE_POLL_MS * 2);
    expect(sent).toEqual([]);
  });
});

describe("data-wallets-kit through createTracker", () => {
  // a separate window, so its watcher ends with it
  function page(gpc = false): Window & typeof globalThis {
    const dom = new JSDOM("<!doctype html><title>dapp</title>", {
      url: "https://dapp.example/",
      // a visible page, so a started tracker sends its pageview (1.3.0)
      pretendToBeVisual: true,
      beforeParse(w) {
        w.fetch = fetch;
        Object.defineProperty(w, "crypto", { value: globalThis.crypto, configurable: true });
        if (gpc) Object.defineProperty(w.navigator, "globalPrivacyControl", { value: true });
      },
    });
    return dom.window as unknown as Window & typeof globalThis;
  }

  beforeEach(() => {
    gw.requests.length = 0;
  });

  it("watches only with the option and only once tracking started", async () => {
    const off = page();
    const on = page();
    createTracker(off, { siteId: SITE, host: gw.origin, autoTrack: false });
    const tracker = createTracker(on, {
      siteId: SITE,
      host: gw.origin,
      autoTrack: false,
      walletsKit: true,
    });
    for (const w of [off, on]) w.localStorage.setItem(KIT_SELECTED_KEY, "xbull");
    await pause(STORAGE_POLL_MS * 2);
    // consent-gated: nothing before start()
    expect(walletEvents()).toEqual([]);
    tracker.start();
    for (const w of [off, on]) {
      w.localStorage.setItem(KIT_SELECTED_KEY, "lobstr");
      w.localStorage.setItem(KIT_ADDRESS_KEY, "present");
    }
    await waitFor(() => walletEvents().length === 2);
    await pause(STORAGE_POLL_MS + QUIET_MS);
    expect(walletEvents()).toEqual([
      { n: WALLET_SELECTED, d: { wallet: "lobstr" } },
      { n: WALLET_CONNECTED, d: undefined },
    ]);
    off.close();
    on.close();
  });

  // gpc: the kit's storage is the visitor's, so a gpc visit must not read it
  it("never reads the kit's storage when start() is called for a gpc visitor", async () => {
    gw.requests.length = 0;
    const w = page(true);
    const tracker = createTracker(w, {
      siteId: SITE,
      host: gw.origin,
      autoTrack: false,
      walletsKit: true,
    });
    const proto = Object.getPrototypeOf(w.localStorage) as Storage;
    let reads = 0;
    const key = proto.key;
    proto.key = function (this: Storage, i: number) {
      reads++;
      return key.call(this, i);
    };
    try {
      // a consent banner that calls start() without checking gpc itself
      tracker.start();
      w.localStorage.setItem(KIT_SELECTED_KEY, "lobstr");
      w.localStorage.setItem(KIT_ADDRESS_KEY, "present");
      await pause(STORAGE_POLL_MS * 2);
      expect(reads).toBe(0);
      expect(gw.requests).toEqual([]);
    } finally {
      proto.key = key;
      w.close();
    }
  });
});

describe("networkName", () => {
  it("maps the three shared passphrases and nothing else", () => {
    expect(networkName("Public Global Stellar Network ; September 2015")).toBe("public");
    expect(networkName("Test SDF Network ; September 2015")).toBe("testnet");
    expect(networkName("Test SDF Future Network ; October 2022")).toBe("futurenet");
    expect(networkName("Standalone Network ; February 2017")).toBe("other");
    expect(networkName("Acme Corp private network")).toBe("other");
    expect(networkName(undefined)).toBe("other");
  });
});

describe("attachWalletsKit without a tracker", () => {
  it("refuses instead of capturing nothing", () => {
    expect(() => attachWalletsKit(kit280.StellarWalletsKit)).toThrow(/needs the tracker/);
  });
});

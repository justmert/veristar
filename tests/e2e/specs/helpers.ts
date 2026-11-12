// test-runner side of the fixture: reads what the fixture gateway recorded and
// holds the checks every test applies to every request it caused.
import { createHash } from "node:crypto";
import { expect, type Page } from "@playwright/test";
import {
  leadingZeroBits,
  type RecordedRequest,
  validateWire,
  type WireEvent,
} from "../fixture/gateway.ts";
import { PORTS, type SiteDef } from "../sites.ts";

const CONTROL = `http://127.0.0.1:${PORTS.control}`;

// the tracker waits 300 ms after a history change before sending; this is how
// long a test waits after reaching the expected count to see that no extra
// event follows
export const SETTLE_MS = 1500;

export async function resetFixture(): Promise<void> {
  const res = await fetch(`${CONTROL}/reset`, { method: "POST" });
  expect(res.ok).toBe(true);
}

export async function recorded(): Promise<RecordedRequest[]> {
  const res = await fetch(`${CONTROL}/requests`);
  expect(res.ok).toBe(true);
  return (await res.json()) as RecordedRequest[];
}

export async function setTampered(version: string, on: boolean): Promise<void> {
  const res = await fetch(`${CONTROL}/tamper?version=${version}&on=${on ? "1" : "0"}`, {
    method: "POST",
  });
  expect(res.ok).toBe(true);
}

export function eventsFor(all: RecordedRequest[], site: SiteDef): RecordedRequest[] {
  return all.filter(
    (r) =>
      r.method === "POST" &&
      r.path === "/v1/e" &&
      (r.json as { w?: string } | null)?.w === site.siteId,
  );
}

export function wire(r: RecordedRequest): WireEvent {
  return r.json as WireEvent;
}

export async function pageviews(site: SiteDef): Promise<WireEvent[]> {
  return eventsFor(await recorded(), site)
    .map(wire)
    .filter((e) => e.t === "pv");
}

export async function customEvents(site: SiteDef): Promise<WireEvent[]> {
  return eventsFor(await recorded(), site)
    .map(wire)
    .filter((e) => e.t === "ev");
}

// waits until exactly n pageviews were recorded, then waits SETTLE_MS more and
// checks that the count did not grow, so a duplicate send fails the test
export async function expectPageviews(site: SiteDef, n: number): Promise<WireEvent[]> {
  await expect.poll(async () => (await pageviews(site)).length, { timeout: 15_000 }).toBe(n);
  await new Promise((r) => setTimeout(r, SETTLE_MS));
  const pvs = await pageviews(site);
  expect(pvs.length, "no extra pageview after settling").toBe(n);
  return pvs;
}

export async function expectCustomEvents(site: SiteDef, n: number): Promise<WireEvent[]> {
  await expect.poll(async () => (await customEvents(site)).length, { timeout: 15_000 }).toBe(n);
  await new Promise((r) => setTimeout(r, SETTLE_MS));
  const evs = await customEvents(site);
  expect(evs.length, "no extra custom event after settling").toBe(n);
  return evs;
}

// the contract checks that hold for every request a test caused:
// no preflight, every event is wire v1 with a proof the fixture accepted, and
// the request is a credential-less cors simple request from the site's origin
export async function expectContract(site: SiteDef): Promise<void> {
  const all = await recorded();
  const options = all.filter((r) => r.method === "OPTIONS");
  expect(options, "no OPTIONS request ever").toEqual([]);

  for (const r of all.filter((x) => x.path === "/v1/e")) {
    expect(r.method).toBe("POST");
    expect(validateWire(r.json), `wire v1 body: ${r.body}`).toBeNull();
    expect(r.verdict, `gateway verdict for ${r.body}`).toEqual({ status: 202, reason: "accepted" });
    const e = wire(r);
    expect(e.w).toBe(site.siteId);
    expect(e.h).toBe(site.host);
    // independent check of the work, apart from the fixture's own verification
    const digest = createHash("sha256")
      .update(`${e.w}|${e.p.iat}|${e.p.salt}|${e.p.nonce}`)
      .digest();
    expect(leadingZeroBits(digest)).toBeGreaterThanOrEqual(e.p.bits);
    expect(r.headers.origin).toBe(site.origin);
    expect(r.headers["content-type"]).toMatch(/^text\/plain(;\s*charset=utf-8)?$/i);
    expect(r.headers.cookie).toBeUndefined();
    expect(r.headers.authorization).toBeUndefined();
  }
  for (const r of all.filter((x) => x.path === "/v1/c")) {
    expect(r.method).toBe("GET");
    expect(r.query.w).toBe(site.siteId);
    expect(r.verdict.status).toBe(200);
    expect(r.headers.cookie).toBeUndefined();
  }
}

// waits for the page to have run the tracker, so a test never starts clicking
// before window.umami exists
export async function trackerReady(page: Page): Promise<void> {
  await page.waitForFunction(() => typeof window.umami?.track === "function");
}

declare global {
  interface Window {
    umami?: { track: (...args: unknown[]) => Promise<void> };
    __saTampered?: boolean;
  }
}

// downloads the current filter lists and checks that the tracker's public urls
// are not blocked when a tenant page loads them. runs on every change and daily,
// because lists change without notice. a list that cannot be downloaded fails
// the test: an unchecked list proves nothing.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { FiltersEngine, Request } from "@ghostery/adblocker";
import { beforeAll, describe, expect, it } from "vitest";

const LISTS: Record<string, string> = {
  easylist: "https://easylist.to/easylist/easylist.txt",
  easyprivacy: "https://easylist.to/easylist/easyprivacy.txt",
  "ublock filters": "https://ublockorigin.github.io/uAssets/filters/filters.txt",
  "ublock privacy": "https://ublockorigin.github.io/uAssets/filters/privacy.txt",
  "ublock badware": "https://ublockorigin.github.io/uAssets/filters/badware.txt",
  "adguard base": "https://filters.adtidy.org/extension/ublock/filters/2.txt",
  "adguard tracking protection": "https://filters.adtidy.org/extension/ublock/filters/3.txt",
  "peter lowe":
    "https://pgl.yoyo.org/adservers/serverlist.php?hostformat=adblockplus&showintro=0&mimetype=plaintext",
};

const manifest = JSON.parse(
  readFileSync(join(import.meta.dirname, "../../packages/tracker/releases/manifest.json"), "utf8"),
) as { versions: { version: string }[] };
const version = manifest.versions.at(-1)?.version;

// the reserved test domain always; the real gateway host once BASE_DOMAIN exists
const hosts = ["in.sa-test.example"];
if (process.env.BASE_DOMAIN) hosts.push(`in.${process.env.BASE_DOMAIN}`);

const TENANT_PAGE = "https://tenant-site.example/pricing";
const TYPES = ["script", "xmlhttprequest", "ping"] as const;

const engines = new Map<string, FiltersEngine>();

// list hosts are small servers that time out or answer 5xx now and then; a
// list is fetched up to three times, and one that never arrives still fails
async function download(name: string, url: string): Promise<string> {
  const failures: string[] = [];
  for (let attempt = 1; attempt <= 3; attempt++) {
    try {
      const res = await fetch(url, {
        headers: { "user-agent": "filter-list-check" },
        signal: AbortSignal.timeout(30_000),
      });
      if (res.ok) return await res.text();
      failures.push(`answered ${res.status}`);
      // a 4xx will not change on a retry
      if (res.status < 500) break;
    } catch (e) {
      const cause = (e as { cause?: unknown }).cause;
      failures.push(`${String(e)}${cause ? ` (${String(cause)})` : ""}`);
    }
    if (attempt < 3) await new Promise((ok) => setTimeout(ok, attempt * 5_000));
  }
  throw new Error(`${name}: ${url} could not be downloaded: ${failures.join("; ")}`);
}

beforeAll(async () => {
  await Promise.all(
    Object.entries(LISTS).map(async ([name, url]) => {
      const text = await download(name, url);
      const rules = text.split("\n").filter((l) => l.trim() !== "" && !l.startsWith("!"));
      if (rules.length < 500)
        throw new Error(`${name}: only ${rules.length} rules, download is suspect`);
      engines.set(name, FiltersEngine.parse(text));
    }),
  );
}, 240000);

describe("filter lists", () => {
  it("has a release to check", () => {
    expect(version).toMatch(/^\d+\.\d+\.\d+$/);
  });

  // positive control: the engine and lists really block a well-known tracker
  it("block google analytics, proving the lists loaded", () => {
    const req = Request.fromRawDetails({
      type: "script",
      url: "https://www.google-analytics.com/analytics.js",
      sourceUrl: TENANT_PAGE,
    });
    const blockedBy = [...engines].filter(([, e]) => e.match(req).match).map(([n]) => n);
    expect(blockedBy.length).toBeGreaterThan(0);
  });

  // the ping request type comes from navigator.sendBeacon and <a ping>; the
  // tracker uses fetch with keepalive, which browsers classify as xmlhttprequest
  it("the released tracker never uses sendBeacon or ping", () => {
    const sdk = readFileSync(
      join(import.meta.dirname, `../../packages/tracker/releases/${version}/sdk.js`),
      "utf8",
    );
    expect(sdk).not.toMatch(/sendBeacon|\.ping\b/);
    expect(sdk).toMatch(/keepalive:!0/);
  });

  for (const host of hosts) {
    const urls = [
      `https://${host}/v/${version}/sdk.js`,
      `https://${host}/v1/c?w=8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b`,
      `https://${host}/v1/e`,
    ];
    for (const url of urls) {
      it(`do not block ${url}`, () => {
        const hits: string[] = [];
        const generic: string[] = [];
        for (const [name, engine] of engines) {
          for (const type of TYPES) {
            const r = engine.match(Request.fromRawDetails({ type, url, sourceUrl: TENANT_PAGE }));
            if (!r.match) continue;
            const filter = r.filter?.toString() ?? "";
            // a rule that is only options, such as easyprivacy's "$ping,3p", blocks
            // every third-party beacon whatever its url, so no name can avoid it.
            // the tracker never sends that request type (checked below).
            if (type === "ping" && filter.startsWith("$")) generic.push(`${name}: ${filter}`);
            else hits.push(`${name} (${type}): ${filter}`);
          }
        }
        expect(hits).toEqual([]);
        for (const g of generic) expect(g).toMatch(/^[a-z ]+: \$ping,(3p|third-party)$/);
      });
    }
  }
});

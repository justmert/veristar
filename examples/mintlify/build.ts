// copies the docs in site/ to dist/ for the browser tests. tracker.js holds
// the example values a tenant replaces with the ones from the console; the
// copy gets the test site id, tracker version and gateway from the
// environment instead, with the integrity value of that version from the
// release manifest.
import { cpSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { releases, scriptUrl } from "tracker/snippet";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`missing env ${name}`);
  return value;
}

// the values site/tracker.js carries, those of the console's example tag
export const EXAMPLE = {
  src: "https://in.analytics.example/v/1.3.1/sdk.js",
  integrity: "sha384-54fz21LNRgy6RLEdhwecMK8yavckH44JnSX9iSA3p2uiEtCEs6fyFt6LhuOL5kKw",
  siteId: "3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c",
};

export function testTracker(source: string): string {
  const version = required("SA_TRACKER_VERSION");
  const entry = releases().find((r) => r.version === version);
  if (!entry) throw new Error(`tracker version ${version} is not in the release manifest`);
  const values: [string, string][] = [
    [EXAMPLE.src, scriptUrl(required("SA_GATEWAY_ORIGIN"), version)],
    [EXAMPLE.integrity, entry.integrity],
    [EXAMPLE.siteId, required("SA_SITE_ID")],
  ];
  let out = source;
  for (const [from, to] of values) {
    if (out.split(`"${from}"`).length !== 2) throw new Error(`tracker.js must hold "${from}" once`);
    out = out.replace(`"${from}"`, `"${to}"`);
  }
  return out;
}

if (import.meta.main) {
  const root = import.meta.dirname;
  const dist = join(root, "dist");
  rmSync(dist, { recursive: true, force: true });
  cpSync(join(root, "site"), dist, { recursive: true });
  const file = join(dist, "tracker.js");
  writeFileSync(file, testTracker(readFileSync(file, "utf8")));
}

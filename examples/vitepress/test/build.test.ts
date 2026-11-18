// builds the site with explicit test values and checks every page carries
// exactly the tag the generator returns for them, once
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { snippet } from "../../../packages/tracker/src/snippet.ts";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e10",
  SA_TRACKER_VERSION: "1.0.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
};

test("vitepress build writes the console's tag into every page", () => {
  const vitepress = join(root, "node_modules", "vitepress", "bin", "vitepress.js");
  execFileSync(process.execPath, [vitepress, "build"], {
    cwd: root,
    env: { ...process.env, ...env },
  });
  const tag = snippet({
    siteId: env.SA_SITE_ID,
    version: env.SA_TRACKER_VERSION,
    origin: env.SA_GATEWAY_ORIGIN,
  });
  for (const page of ["index.html", "pricing.html", "docs/getting-started.html"]) {
    const html = readFileSync(join(root, ".vitepress", "dist", page), "utf8");
    assert.equal(html.split(tag).length, 2, page);
  }
});

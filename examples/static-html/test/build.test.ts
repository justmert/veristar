// builds the site with explicit test values and checks every page carries
// exactly the snippet the generator returns for them, once
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { snippet } from "tracker/snippet";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e01",
  SA_TRACKER_VERSION: "1.0.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
};

test("build writes the snippet into every page", () => {
  execFileSync(process.execPath, ["build.ts"], { cwd: root, env: { ...process.env, ...env } });
  const tag = snippet({
    siteId: env.SA_SITE_ID,
    version: env.SA_TRACKER_VERSION,
    origin: env.SA_GATEWAY_ORIGIN,
  });
  for (const page of ["index.html", "about.html"]) {
    const html = readFileSync(join(root, "dist", page), "utf8");
    assert.equal(html.split(tag).length, 2, page);
    assert.equal(html.match(/<script defer src=/g)?.length, 1, page);
  }
});

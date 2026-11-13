// builds the spa with explicit test values and checks the html shell carries
// exactly the snippet the generator returns for them, once
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { snippet } from "tracker/snippet";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e06",
  SA_TRACKER_VERSION: "1.0.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
};

test("vite build writes the snippet into index.html", () => {
  const vite = join(root, "node_modules", "vite", "bin", "vite.js");
  execFileSync(process.execPath, [vite, "build"], { cwd: root, env: { ...process.env, ...env } });
  const html = readFileSync(join(root, "dist", "index.html"), "utf8");
  const tag = snippet({
    siteId: env.SA_SITE_ID,
    version: env.SA_TRACKER_VERSION,
    origin: env.SA_GATEWAY_ORIGIN,
  });
  assert.equal(html.split(tag).length, 2);
});

// site/tracker.js carries the values of the console's example tag, and the
// build puts the test values in their place, each exactly once
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { releases, scriptUrl } from "tracker/snippet";
import { EXAMPLE } from "../build.ts";

const root = join(import.meta.dirname, "..");

test("tracker.js holds the example tag's values", () => {
  const entry = releases().find((r) => r.version === "1.3.1");
  assert.ok(entry);
  assert.equal(EXAMPLE.src, scriptUrl("https://in.analytics.example", "1.3.1"));
  assert.equal(EXAMPLE.integrity, entry.integrity);
  const source = readFileSync(join(root, "site", "tracker.js"), "utf8");
  assert.ok(source.includes(`tracker.src = "${EXAMPLE.src}";`));
  assert.ok(source.includes(`tracker.integrity = "${EXAMPLE.integrity}";`));
  assert.ok(source.includes(`tracker.crossOrigin = "anonymous";`));
  assert.ok(source.includes(`tracker.dataset.websiteId = "${EXAMPLE.siteId}";`));
});

test("the build writes the test values into dist/tracker.js", () => {
  const env = {
    SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e11",
    SA_TRACKER_VERSION: "1.0.0",
    SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
  };
  execFileSync(process.execPath, [join(root, "build.ts")], {
    cwd: root,
    env: { ...process.env, ...env },
  });
  const entry = releases().find((r) => r.version === env.SA_TRACKER_VERSION);
  assert.ok(entry);
  const built = readFileSync(join(root, "dist", "tracker.js"), "utf8");
  assert.ok(built.includes(`"${scriptUrl(env.SA_GATEWAY_ORIGIN, env.SA_TRACKER_VERSION)}"`));
  assert.ok(built.includes(`"${entry.integrity}"`));
  assert.ok(built.includes(`"${env.SA_SITE_ID}"`));
  for (const value of Object.values(EXAMPLE)) assert.ok(!built.includes(value));
  // the content files are copied unchanged
  assert.equal(
    readFileSync(join(root, "dist", "index.mdx"), "utf8"),
    readFileSync(join(root, "site", "index.mdx"), "utf8"),
  );
});

// builds the site with explicit test values and checks every page carries
// exactly the tag the generator returns for them, once, and that the
// self-hosted copy of sdk.js is the release file byte for byte
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { releases, snippet } from "tracker/snippet";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e01",
  SA_TRACKER_VERSION: "1.0.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
};

test("build writes the right tag into every page", () => {
  execFileSync(process.execPath, ["build.ts"], { cwd: root, env: { ...process.env, ...env } });
  const tag = snippet({
    siteId: env.SA_SITE_ID,
    version: env.SA_TRACKER_VERSION,
    origin: env.SA_GATEWAY_ORIGIN,
  });
  const open = tag.slice(0, -"></script>".length);
  const expected: Record<string, string> = {
    "index.html": tag,
    "about.html": tag,
    "consent.html": `${open} data-auto-track="false"></script>`,
    "self-hosted.html": `${open.replace(
      `src="${env.SA_GATEWAY_ORIGIN}/v/1.0.0/sdk.js"`,
      'src="/sdk/1.0.0/sdk.js"',
    )} data-host-url="${env.SA_GATEWAY_ORIGIN}"></script>`,
  };
  for (const [page, want] of Object.entries(expected)) {
    const html = readFileSync(join(root, "dist", page), "utf8");
    assert.equal(html.split(want).length, 2, page);
    assert.equal(html.match(/<script defer src=/g)?.length, page === "consent.html" ? 2 : 1, page);
  }
});

test("the self-hosted sdk.js matches the release integrity", () => {
  const entry = releases().find((r) => r.version === env.SA_TRACKER_VERSION);
  assert.ok(entry);
  const body = readFileSync(join(root, "dist", "sdk", "1.0.0", "sdk.js"));
  const digest = createHash("sha384").update(body).digest("base64");
  assert.equal(`sha384-${digest}`, entry.integrity);
});

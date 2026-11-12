// builds the example both ways and checks what each build contains: the
// snippet with data-wallets-kit="true" in index.html, and the test wallet
// module of tests/fixtures only in the wallettest build
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { snippet } from "tracker/snippet";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e05",
  SA_TRACKER_VERSION: "1.1.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
};
const vite = join(root, "node_modules", "vite", "bin", "vite.js");

function build(mode: string): { html: string; assets: string } {
  execFileSync(process.execPath, [vite, "build", "--mode", mode], {
    cwd: root,
    env: { ...process.env, ...env },
  });
  const dir = join(root, "dist", "assets");
  const assets = readdirSync(dir)
    .map((f) => readFileSync(join(dir, f), "utf8"))
    .join("\n");
  return { html: readFileSync(join(root, "dist", "index.html"), "utf8"), assets };
}

test("a normal build carries the opt-in snippet and no test wallet", () => {
  const { html, assets } = build("production");
  const tag = snippet({
    siteId: env.SA_SITE_ID,
    version: env.SA_TRACKER_VERSION,
    origin: env.SA_GATEWAY_ORIGIN,
    walletsKit: true,
  });
  assert.equal(html.split(tag).length, 2);
  assert.ok(tag.endsWith(' data-wallets-kit="true"></script>'));
  assert.ok(!assets.includes("sa-test-wallet"), "test wallet module in a normal build");
  assert.ok(assets.includes("@StellarWalletsKit/activeAddress"), "the real kit is bundled");
});

test("the wallettest build adds the test wallet", () => {
  const { assets } = build("wallettest");
  assert.ok(assets.includes("sa-test-wallet"));
});

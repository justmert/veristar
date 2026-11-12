// builds the site with explicit test values, starts the standalone server and
// checks that a server-rendered page carries exactly the generated snippet
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { join } from "node:path";
import { test } from "node:test";
import { snippet } from "tracker/snippet";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e04",
  SA_TRACKER_VERSION: "1.0.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
};

test("server rendered html carries the snippet once", async () => {
  execFileSync(
    process.execPath,
    [join(root, "node_modules", "astro", "bin", "astro.mjs"), "build"],
    {
      cwd: root,
      env: { ...process.env, ...env },
    },
  );
  const port = 47194;
  const server = spawn(process.execPath, ["./dist/server/entry.mjs"], {
    cwd: root,
    env: { ...process.env, ...env, PORT: String(port), HOST: "127.0.0.1" },
  });
  try {
    const html = await fetchWhenUp(`http://127.0.0.1:${port}/docs/intro`);
    const tag = snippet({
      siteId: env.SA_SITE_ID,
      version: env.SA_TRACKER_VERSION,
      origin: env.SA_GATEWAY_ORIGIN,
    });
    assert.equal(html.split(tag).length, 2);
  } finally {
    server.kill();
  }
});

async function fetchWhenUp(url: string): Promise<string> {
  const deadline = Date.now() + 20_000;
  for (;;) {
    try {
      const res = await fetch(url);
      assert.equal(res.status, 200);
      return await res.text();
    } catch (err) {
      if (Date.now() > deadline) throw err;
      await new Promise((r) => setTimeout(r, 200));
    }
  }
}

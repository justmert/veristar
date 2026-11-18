// builds the app with explicit test values, starts the production server and
// checks that a server-rendered page carries the tracker tag with the release's
// integrity value
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { join } from "node:path";
import { test } from "node:test";
import { releases, scriptUrl } from "tracker/snippet";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e08",
  SA_TRACKER_VERSION: "1.0.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
  NEXT_TELEMETRY_DISABLED: "1",
};
const next = join(root, "node_modules", "next", "dist", "bin", "next");

test("server rendered html carries the tracker tag", async () => {
  execFileSync(process.execPath, [next, "build"], { cwd: root, env: { ...process.env, ...env } });
  const port = 27196;
  const server = spawn(
    process.execPath,
    [next, "start", "--hostname", "127.0.0.1", "--port", String(port)],
    { cwd: root, env: { ...process.env, ...env } },
  );
  try {
    const html = await fetchWhenUp(`http://127.0.0.1:${port}/pricing`);
    const entry = releases().find((r) => r.version === env.SA_TRACKER_VERSION);
    assert.ok(entry);
    assert.ok(html.includes(`src="${scriptUrl(env.SA_GATEWAY_ORIGIN, env.SA_TRACKER_VERSION)}"`));
    assert.ok(html.includes(`integrity="${entry.integrity}"`));
    assert.ok(html.includes(`crossorigin="anonymous"`));
    assert.ok(html.includes(`data-website-id="${env.SA_SITE_ID}"`));
  } finally {
    server.kill();
  }
});

async function fetchWhenUp(url: string): Promise<string> {
  const deadline = Date.now() + 30_000;
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

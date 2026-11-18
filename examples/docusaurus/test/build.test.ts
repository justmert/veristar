// builds the site with explicit test values and checks every page carries
// one script element with each attribute of the tag the generator returns
// for them. docusaurus writes the attributes in its own order and defer as
// defer="defer", which the browser reads the same way
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { releases, scriptUrl } from "../../../packages/tracker/src/snippet.ts";

const root = join(import.meta.dirname, "..");
const env = {
  SA_SITE_ID: "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e09",
  SA_TRACKER_VERSION: "1.0.0",
  SA_GATEWAY_ORIGIN: "https://in.sa-test.example",
};

test("docusaurus build writes the console's tag into every page", () => {
  const docusaurus = join(root, "node_modules", "@docusaurus", "core", "bin", "docusaurus.mjs");
  execFileSync(process.execPath, [docusaurus, "build"], {
    cwd: root,
    env: { ...process.env, ...env },
    stdio: "ignore",
  });
  const entry = releases().find((r) => r.version === env.SA_TRACKER_VERSION);
  assert.ok(entry);
  const src = scriptUrl(env.SA_GATEWAY_ORIGIN, env.SA_TRACKER_VERSION);
  for (const page of ["index.html", "pricing/index.html", "docs/getting-started/index.html"]) {
    const html = readFileSync(join(root, "build", page), "utf8");
    const tags = html.match(/<script [^>]*sdk\.js[^>]*>/g) ?? [];
    assert.equal(tags.length, 1, page);
    const tag = tags[0] as string;
    for (const attr of [
      `src="${src}"`,
      `defer="defer"`,
      `integrity="${entry.integrity}"`,
      `crossorigin="anonymous"`,
      `data-website-id="${env.SA_SITE_ID}"`,
    ]) {
      assert.ok(tag.includes(` ${attr}`), `${page}: ${attr} in ${tag}`);
    }
  }
});

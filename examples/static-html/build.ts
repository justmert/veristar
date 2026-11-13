// writes dist/ from src/: each page gets a tracker tag where its marker
// comment stands. a tenant with a hand-written site pastes the tag there
// instead; this script only exists so the test site id and gateway origin can
// come from the environment.
//   <!-- tracker -->              the tag from the console
//   <!-- tracker:consent -->      the tag with data-auto-track="false"
//   <!-- tracker:self-hosted -->  the tag pointing at a copy of sdk.js on this site
import { copyFileSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { consentTag, releaseFile, selfHostedPath, selfHostedTag, trackerTag } from "./tracker.ts";

const here = dirname(fileURLToPath(import.meta.url));
const src = join(here, "src");
const out = join(here, "dist");
const markers: Record<string, () => string> = {
  "<!-- tracker -->": trackerTag,
  "<!-- tracker:consent -->": consentTag,
  "<!-- tracker:self-hosted -->": selfHostedTag,
};

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
for (const name of readdirSync(src)) {
  const text = readFileSync(join(src, name), "utf8");
  if (name.endsWith(".html")) {
    const found = Object.keys(markers).filter((m) => text.includes(m));
    const marker = found[0];
    if (found.length !== 1 || !marker || text.split(marker).length !== 2) {
      throw new Error(`${name} must contain exactly one tracker marker, once`);
    }
    writeFileSync(join(out, name), text.replace(marker, markers[marker]?.() ?? ""));
  } else {
    writeFileSync(join(out, name), text);
  }
}

// the self-hosted copy: the release file byte for byte, so its integrity
// value is unchanged
const selfHosted = join(out, selfHostedPath());
mkdirSync(dirname(selfHosted), { recursive: true });
copyFileSync(join(here, "node_modules", "tracker", "releases", releaseFile()), selfHosted);
console.log(`static-html: wrote ${out}`);

// writes dist/ from src/: each page gets the tracker snippet where the
// <!-- tracker --> comment stands. a tenant with a hand-written site pastes the
// snippet there instead; this script only exists so the test site id and
// gateway origin can come from the environment.
import { mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { trackerTag } from "./tracker.ts";

const MARKER = "<!-- tracker -->";
const here = dirname(fileURLToPath(import.meta.url));
const src = join(here, "src");
const out = join(here, "dist");
const tag = trackerTag();

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
for (const name of readdirSync(src)) {
  const text = readFileSync(join(src, name), "utf8");
  if (name.endsWith(".html")) {
    const parts = text.split(MARKER);
    if (parts.length !== 2) throw new Error(`${name} must contain ${MARKER} exactly once`);
    writeFileSync(join(out, name), parts.join(tag));
  } else {
    writeFileSync(join(out, name), text);
  }
}
console.log(`static-html: wrote ${out}`);

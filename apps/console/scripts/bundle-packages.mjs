// lists every npm package whose javascript ends up in the console bundle, as
// name@version, one per line. the licence gate (scripts/supply-chain/licences.py
// --console-bundle) fails when one of them is not in its shipped section, so
// a package imported at runtime but declared as a devDependency cannot reach
// the bundle unscanned.
//
//   node apps/console/scripts/bundle-packages.mjs > console-bundle.txt
//
// it runs the console's own vite config into a temporary directory, so dist/
// is not touched. css packages (tailwindcss, tw-animate-css, shadcn's css,
// the geist font) are compiled by the tailwind plugin and are not modules of
// the graph; docs/security/licence-policy.json names them instead.

import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "vite";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const out = mkdtempSync(join(tmpdir(), "console-bundle-"));
const ids = new Set();
try {
  await build({
    root,
    logLevel: "silent",
    build: { outDir: out, emptyOutDir: true },
    plugins: [
      {
        name: "list-modules",
        enforce: "post",
        generateBundle() {
          // only modules that are part of an emitted chunk count; getModuleIds
          // also covers everything the graph loaded, which is the safe side
          for (const id of this.getModuleIds()) ids.add(id);
        },
      },
    ],
  });
} finally {
  rmSync(out, { recursive: true, force: true });
}

const pkgs = new Set();
for (const id of ids) {
  const i = id.lastIndexOf("/node_modules/");
  if (i < 0) continue;
  const rest = id.slice(i + "/node_modules/".length).split("/");
  const name = rest[0].startsWith("@") ? `${rest[0]}/${rest[1]}` : rest[0];
  const manifest = join(id.slice(0, i), "node_modules", name, "package.json");
  pkgs.add(`${name}@${JSON.parse(readFileSync(manifest, "utf8")).version}`);
}
if (pkgs.size === 0)
  throw new Error("the console bundle has no npm packages, which cannot be right");
process.stdout.write(`${[...pkgs].sort().join("\n")}\n`);

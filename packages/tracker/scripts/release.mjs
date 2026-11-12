// pnpm --filter tracker release <version>
// builds sdk.js for a new version, writes releases/<version>/sdk.js and appends
// the manifest entry. it never touches an existing release.

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { build, ESBUILD_VERSION, packageVersion } from "../build.mjs";
import { checkConsistency, compareVersions, newest, RELEASES, readManifest } from "./lib.mjs";

const version = process.argv[2];
if (!version) {
  console.error("usage: pnpm --filter tracker release <version>");
  process.exit(2);
}

function fail(message) {
  console.error(`release refused: ${message}`);
  process.exit(1);
}

// the esm entry reads the same version, so package.json is bumped first
if (packageVersion() !== version)
  fail(`package.json version is ${packageVersion()}; set it to ${version} first`);

const before = checkConsistency();
if (before.length > 0) fail(`releases/ is inconsistent before release:\n${before.join("\n")}`);

const manifest = readManifest();
const last = newest(manifest);
if (manifest.versions.some((v) => v.version === version)) fail(`${version} is already released`);
if (last && compareVersions(version, last.version) <= 0)
  fail(`${version} is not newer than ${last.version}`);

const out = await build(version);
const dir = join(RELEASES, version);
mkdirSync(dir);
// flag wx: refuse to overwrite, even if the directory was created by hand
writeFileSync(join(dir, "sdk.js"), out.bytes, { flag: "wx" });

const entry = {
  version,
  file: `${version}/sdk.js`,
  bytes: out.size,
  gzipBytes: out.gzipSize,
  sha384: out.sha384,
  integrity: out.integrity,
  esbuild: ESBUILD_VERSION,
  released: new Date().toISOString().slice(0, 10),
};
manifest.versions.push(entry);
writeFileSync(join(RELEASES, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);

const after = checkConsistency();
if (after.length > 0) fail(`releases/ is inconsistent after release:\n${after.join("\n")}`);

if (last) {
  const growth = (out.gzipSize - last.gzipBytes) / last.gzipBytes;
  console.log(`gzip size ${last.gzipBytes} -> ${out.gzipSize} (${(growth * 100).toFixed(1)}%)`);
}
console.log(JSON.stringify(entry, null, 2));

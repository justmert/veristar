// builds dist/sdk.js. the output must depend only on the source and the pinned
// esbuild version: no timestamps, no absolute paths, no source maps, a fixed
// target. scripts/verify-releases.mjs rebuilds the newest release and compares
// bytes, so anything nondeterministic here breaks ci.

import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import * as esbuild from "esbuild";

export const ROOT = dirname(fileURLToPath(import.meta.url));
export const ESBUILD_VERSION = esbuild.version;
const PINNED_ESBUILD = "0.28.2";
const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

export function packageVersion() {
  return JSON.parse(readFileSync(join(ROOT, "package.json"), "utf8")).version;
}

export async function build(version) {
  if (!SEMVER.test(version)) throw new Error(`version ${version} is not x.y.z`);
  // a different esbuild would produce different bytes for the same release
  if (esbuild.version !== PINNED_ESBUILD) {
    throw new Error(`esbuild ${esbuild.version} is installed, the build needs ${PINNED_ESBUILD}`);
  }
  const result = await esbuild.build({
    absWorkingDir: ROOT,
    entryPoints: ["src/script.ts"],
    bundle: true,
    format: "iife",
    platform: "browser",
    target: ["es2020"],
    minify: true,
    sourcemap: false,
    charset: "ascii",
    legalComments: "inline",
    define: { __SDK_VERSION__: JSON.stringify(version) },
    write: false,
    logLevel: "silent",
    metafile: true,
  });
  if (result.errors.length > 0 || result.warnings.length > 0) {
    throw new Error(`esbuild reported problems: ${JSON.stringify(result.warnings)}`);
  }
  ownInputsOnly(result.metafile);
  const file = result.outputFiles[0];
  if (!file) throw new Error("esbuild produced no output");
  return describe(Buffer.from(file.contents));
}

// sdk.js and the npm package carry only this package's own source and release
// files. the licence gate (docs/security/supply-chain.md section 7) relies on
// that: the tracker has no runtime dependencies, so a third-party module
// bundled in by accident fails the build instead of shipping unscanned.
export function ownInputsOnly(metafile) {
  const foreign = Object.keys(metafile.inputs).filter((i) => !/^(src|releases)\//.test(i));
  if (foreign.length > 0) {
    throw new Error(`the tracker bundle must not include other packages: ${foreign.join(", ")}`);
  }
}

export function describe(bytes) {
  const digest = createHash("sha384").update(bytes).digest();
  return {
    bytes,
    size: bytes.length,
    gzipSize: gzipSync(bytes, { level: 9 }).length,
    sha384: digest.toString("hex"),
    integrity: `sha384-${digest.toString("base64")}`,
  };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const version = process.argv[2] ?? packageVersion();
  const out = await build(version);
  mkdirSync(join(ROOT, "dist"), { recursive: true });
  writeFileSync(join(ROOT, "dist", "sdk.js"), out.bytes);
  console.log(
    JSON.stringify(
      {
        version,
        file: "dist/sdk.js",
        bytes: out.size,
        gzipBytes: out.gzipSize,
        sha384: out.sha384,
        integrity: out.integrity,
      },
      null,
      2,
    ),
  );
}

// builds the npm package of the tracker into
// npm/<version>/ and packs it with `npm pack`. the source package.json exports
// the typescript sources, which only work when the consumer's bundler defines
// __SDK_VERSION__; the published package instead carries esm javascript with
// the version compiled in, declaration files, the immutable release files and
// the licence.
//
//   NPM_PACKAGE=@scope/name node scripts/pack.mjs [version]
//
// NPM_PACKAGE is the npm scope that has trusted publishing set up. it is
// required and never guessed, so nothing can be packed under a made-up name.
// the version must be a released version in releases/manifest.json, so the
// sdk.js inside the package is byte-identical to the immutable release.
//
// the output depends only on the source, the pinned esbuild and typescript and
// the inputs, and `npm pack` writes fixed timestamps, so two runs give the
// same tarball; release-tracker.yml checks that before publishing.

import { execFileSync } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import * as esbuild from "esbuild";
import { ESBUILD_VERSION, ownInputsOnly, packageVersion, ROOT } from "../build.mjs";
import { readManifest } from "./lib.mjs";

const PINNED_ESBUILD = "0.28.2";
const NAME = /^@[a-z0-9][a-z0-9._-]*\/[a-z0-9][a-z0-9._-]*$/;
// the public entry points; each one is a subpath export of the package
const ENTRIES = ["index", "snippet", "wire", "wallets-kit", "proof"];

export async function pack(name, version, outRoot = join(ROOT, "npm")) {
  if (!NAME.test(name ?? "")) {
    throw new Error("NPM_PACKAGE must be a scoped npm name, for example @scope/tracker");
  }
  if (ESBUILD_VERSION !== PINNED_ESBUILD) {
    throw new Error(`esbuild ${ESBUILD_VERSION} is installed, the build needs ${PINNED_ESBUILD}`);
  }
  const entry = readManifest().versions.find((v) => v.version === version);
  if (!entry) throw new Error(`${version} is not a released version in releases/manifest.json`);

  const out = join(outRoot, version);
  rmSync(out, { recursive: true, force: true });
  mkdirSync(join(out, "dist"), { recursive: true });

  // esm javascript: one file per entry, shared code in chunks, the version
  // compiled in. not minified, so tenants can read what they ship.
  const result = await esbuild.build({
    absWorkingDir: ROOT,
    entryPoints: ENTRIES.map((e) => ({ in: `src/${e}.ts`, out: e })),
    outdir: join(out, "dist"),
    bundle: true,
    splitting: true,
    format: "esm",
    platform: "browser",
    target: ["es2020"],
    minify: false,
    sourcemap: false,
    charset: "ascii",
    legalComments: "inline",
    chunkNames: "chunk-[hash]",
    define: { __SDK_VERSION__: JSON.stringify(version) },
    write: true,
    logLevel: "silent",
    metafile: true,
  });
  if (result.errors.length > 0 || result.warnings.length > 0) {
    throw new Error(`esbuild reported problems: ${JSON.stringify(result.warnings)}`);
  }
  ownInputsOnly(result.metafile);

  // declaration files from the same sources; the .ts import suffixes are
  // rewritten to .js so the declarations resolve next to the javascript
  execFileSync(
    join(ROOT, "node_modules", ".bin", "tsc"),
    [
      // the flags below are the whole configuration of this emit
      "--ignoreConfig",
      "--declaration",
      "--emitDeclarationOnly",
      "--noEmit",
      "false",
      "--outDir",
      join(out, "dist"),
      "--rootDir",
      "src",
      "--target",
      "ES2023",
      "--module",
      "ESNext",
      "--moduleResolution",
      "bundler",
      "--lib",
      "ES2023,DOM,DOM.Iterable",
      "--types",
      "",
      "--strict",
      "--skipLibCheck",
      "--allowImportingTsExtensions",
      "--verbatimModuleSyntax",
      ...ENTRIES.map((e) => `src/${e}.ts`),
    ],
    { cwd: ROOT, stdio: "inherit" },
  );

  const files = walk(join(out, "dist"));
  for (const f of files) {
    // tsc keeps the source's ./x.ts specifiers in declarations; only the .js
    // files are shipped, so the declarations must name those
    if (f.endsWith(".d.ts")) {
      const src = readFileSync(f, "utf8");
      writeFileSync(f, src.replace(/(from |import\()"(\.\.?\/[^"]+)\.ts"/g, '$1"$2.js"'));
    }
    const text = readFileSync(f, "utf8");
    // the reason this build exists: no consumer has to define it anymore
    if (text.includes("__SDK_VERSION__"))
      throw new Error(`${relative(out, f)} still names __SDK_VERSION__`);
    // declarations must not point at .ts files that are not shipped
    if (f.endsWith(".d.ts") && /from "\.[^"]*\.ts"/.test(text)) {
      throw new Error(`${relative(out, f)} imports a .ts path`);
    }
  }
  for (const e of ENTRIES) {
    for (const ext of [".js", ".d.ts"]) {
      if (!existsSync(join(out, "dist", e + ext)))
        throw new Error(`dist/${e}${ext} was not written`);
    }
  }

  // the immutable release files travel with the package, so a tenant can
  // self-host sdk.js from the registry with the same integrity hash
  cpSync(join(ROOT, "releases"), join(out, "releases"), { recursive: true });
  cpSync(join(ROOT, "LICENSE"), join(out, "LICENSE"));

  const exp = (e) => ({ types: `./dist/${e}.d.ts`, default: `./dist/${e}.js` });
  const pkg = {
    name,
    version,
    description: "browser tracker for veristar, derived from the umami tracker",
    license: "MIT",
    type: "module",
    // npm provenance checks that this matches the repository that built it
    repository: {
      type: "git",
      url: "git+https://github.com/justmert/veristar.git",
      directory: "packages/tracker",
    },
    exports: {
      ".": exp("index"),
      "./snippet": exp("snippet"),
      "./wire": exp("wire"),
      "./wallets-kit": exp("wallets-kit"),
      "./proof": exp("proof"),
      "./releases/manifest.json": "./releases/manifest.json",
      "./releases/revoked.json": "./releases/revoked.json",
      [`./releases/${version}/sdk.js`]: `./releases/${version}/sdk.js`,
    },
    files: ["dist", "releases", "LICENSE"],
    sideEffects: false,
    publishConfig: { access: "public", provenance: true },
  };
  writeFileSync(join(out, "package.json"), `${JSON.stringify(pkg, null, 2)}\n`);

  const packed = JSON.parse(
    execFileSync("npm", ["pack", "--json", "--pack-destination", outRoot], {
      cwd: out,
      encoding: "utf8",
    }),
  )[0];
  return {
    dir: out,
    tarball: join(outRoot, packed.filename),
    integrity: packed.integrity,
    shasum: packed.shasum,
  };
}

function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((d) =>
    d.isDirectory() ? walk(join(dir, d.name)) : [join(dir, d.name)],
  );
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const version = process.argv[2] ?? packageVersion();
  const r = await pack(process.env.NPM_PACKAGE, version);
  console.log(
    JSON.stringify(
      { ...r, dir: relative(ROOT, r.dir), tarball: relative(ROOT, r.tarball) },
      null,
      2,
    ),
  );
}

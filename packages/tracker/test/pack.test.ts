// the npm package build (scripts/pack.mjs): the published esm entry must work
// without a bundler define, ship working declarations, carry the immutable
// sdk.js and pack to the same bytes twice. the package name here is a
// test-only scope, because the real one is an operator input and does not exist yet.

import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import * as esbuild from "esbuild";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
// @ts-expect-error plain javascript module without declarations
import { newest, readManifest } from "../scripts/lib.mjs";
// @ts-expect-error plain javascript module without declarations
import { pack } from "../scripts/pack.mjs";

const NAME = "@sa-local-test/tracker";
const ROOT = join(import.meta.dirname, "..");

describe("npm package", () => {
  let work: string;
  let first: { tarball: string; integrity: string };
  let consumer: string;
  const version: string = newest(readManifest()).version;

  beforeAll(async () => {
    work = mkdtempSync(join(tmpdir(), "tracker-pack-"));
    first = await pack(NAME, version, join(work, "a"));
    consumer = join(work, "consumer");
    execFileSync("npm", ["init", "-y"], { cwd: work, stdio: "ignore" });
    writeFileSync(
      join(work, "package.json"),
      JSON.stringify({ name: "consumer", private: true, type: "module" }),
    );
    execFileSync("npm", ["install", "--no-audit", "--no-fund", "--offline", first.tarball], {
      cwd: work,
      stdio: "ignore",
    });
    consumer = work;
  }, 120000);

  afterAll(() => rmSync(work, { recursive: true, force: true }));

  it("packs to the same bytes twice", async () => {
    const second = await pack(NAME, version, join(work, "b"));
    expect(second.integrity).toBe(first.integrity);
  }, 120000);

  it("needs no __SDK_VERSION__ define in the consumer", async () => {
    const mod = await import(join(consumer, "node_modules", NAME, "dist", "index.js"));
    expect(mod.VERSION).toBe(version);
    expect(typeof mod.init).toBe("function");
    // a bundler with no define at all, as a tenant's build would be
    writeFileSync(
      join(consumer, "entry.js"),
      `import { VERSION, init } from "${NAME}";\nconsole.log(VERSION, init);\n`,
    );
    const r = await esbuild.build({
      absWorkingDir: consumer,
      entryPoints: ["entry.js"],
      bundle: true,
      format: "esm",
      platform: "browser",
      write: false,
      logLevel: "silent",
    });
    const text = r.outputFiles[0]?.text ?? "";
    expect(text).not.toContain("__SDK_VERSION__");
    expect(text).toContain(JSON.stringify(version));
  });

  it("ships declarations that type-check in a consumer", () => {
    writeFileSync(
      join(consumer, "use.ts"),
      `import { init, VERSION, type Tracker } from "${NAME}";\nimport { snippet } from "${NAME}/snippet";\nconst v: string = VERSION;\nconst t: (o: Parameters<typeof init>[0]) => Tracker = init;\nvoid v; void t; void snippet;\n`,
    );
    execFileSync(
      join(ROOT, "node_modules", ".bin", "tsc"),
      [
        "--ignoreConfig",
        "--noEmit",
        "--strict",
        "--module",
        "ESNext",
        "--moduleResolution",
        "bundler",
        "--target",
        "ES2023",
        "--lib",
        "ES2023,DOM",
        "--types",
        "",
        "use.ts",
      ],
      { cwd: consumer, stdio: "pipe" },
    );
  });

  it("carries the immutable sdk.js byte for byte", () => {
    const shipped = readFileSync(
      join(consumer, "node_modules", NAME, "releases", version, "sdk.js"),
    );
    expect(shipped.equals(readFileSync(join(ROOT, "releases", version, "sdk.js")))).toBe(true);
  });

  it("refuses a missing or unscoped package name and an unreleased version", async () => {
    await expect(pack(undefined, version, join(work, "c"))).rejects.toThrow(/I9/);
    await expect(pack("tracker", version, join(work, "c"))).rejects.toThrow(/I9/);
    await expect(pack(NAME, "9.9.9", join(work, "c"))).rejects.toThrow(/not a released version/);
  });
});

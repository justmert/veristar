import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

type Lib = {
  RELEASES: string;
  checkConsistency: (dir?: string) => string[];
  checkAgainstBase: (repo: string, releasesPath: string, ref: string) => string[];
  checkReproducible: () => Promise<string[]>;
  checkSize: () => Promise<string[]>;
};
// the release tooling is plain javascript run by node, so it is imported untyped
const lib = (await import("../scripts/lib.mjs" as string)) as Lib;
const ROOT = join(import.meta.dirname, "..");

describe("committed releases", () => {
  it("are consistent with the manifest", () => {
    expect(lib.checkConsistency()).toEqual([]);
  });

  it("rebuild byte-identically from the current source", async () => {
    expect(await lib.checkReproducible()).toEqual([]);
  });

  it("stay within the size budget", async () => {
    expect(await lib.checkSize()).toEqual([]);
  });

  it("pass the verify-releases command against HEAD", () => {
    const out = execFileSync(process.execPath, [join(ROOT, "scripts/verify-releases.mjs")], {
      encoding: "utf8",
    });
    expect(out).toMatch(/releases verified/);
  });
});

// proves the immutability check on a throwaway repository: a copy of the
// release directory is committed, then edited the ways a mistake would edit it
describe("immutability check", () => {
  let repo: string;
  let rel: string;
  const relPath = "packages/tracker/releases";
  const git = (...args: string[]) =>
    execFileSync(
      "git",
      ["-C", repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", ...args],
      { stdio: "ignore" },
    );

  beforeAll(() => {
    repo = mkdtempSync(join(tmpdir(), "tracker-releases-"));
    rel = join(repo, relPath);
    cpSync(lib.RELEASES, rel, { recursive: true });
    git("init", "-q", "-b", "main");
    git("add", ".");
    git("commit", "-q", "-m", "base");
  });

  afterAll(() => rmSync(repo, { recursive: true, force: true }));

  const restore = () => git("checkout", "-q", "--", ".");

  it("passes on an unchanged tree", () => {
    expect(lib.checkAgainstBase(repo, relPath, "HEAD")).toEqual([]);
  });

  it("fails when a committed sdk.js is edited", () => {
    const file = join(rel, "1.0.0", "sdk.js");
    writeFileSync(file, `${readFileSync(file, "utf8")};`);
    expect(lib.checkAgainstBase(repo, relPath, "HEAD")).toEqual([
      `${relPath}/1.0.0/sdk.js was changed`,
    ]);
    expect(lib.checkConsistency(rel).length).toBeGreaterThan(0);
    restore();
  });

  it("fails when a committed manifest entry is edited", () => {
    const file = join(rel, "manifest.json");
    const m = JSON.parse(readFileSync(file, "utf8"));
    m.versions[0].integrity = "sha384-AAAA";
    writeFileSync(file, JSON.stringify(m));
    expect(lib.checkAgainstBase(repo, relPath, "HEAD")).toEqual([
      "manifest entry 1.0.0 was changed, moved or removed",
    ]);
    restore();
  });

  it("fails when a committed release is removed", () => {
    rmSync(join(rel, "1.0.0"), { recursive: true });
    expect(lib.checkAgainstBase(repo, relPath, "HEAD")).toEqual([
      `${relPath}/1.0.0/sdk.js was removed`,
    ]);
    restore();
  });

  it("allows a new manifest entry and an edited revoked.json", () => {
    const file = join(rel, "manifest.json");
    const m = JSON.parse(readFileSync(file, "utf8"));
    m.versions.push({ ...m.versions[0], version: "1.0.1", file: "1.0.1/sdk.js" });
    writeFileSync(file, JSON.stringify(m));
    writeFileSync(
      join(rel, "revoked.json"),
      JSON.stringify({
        revoked: [{ version: "1.0.0", reason: "fixture", revokedAt: "2026-11-12" }],
      }),
    );
    expect(lib.checkAgainstBase(repo, relPath, "HEAD")).toEqual([]);
    restore();
  });
});

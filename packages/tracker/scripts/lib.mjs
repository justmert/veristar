// checks and helpers for the immutable release set under releases/.
// releases/<version>/sdk.js and every entry of releases/manifest.json never
// change once committed. releases/revoked.json is the one mutable file.

import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { build, describe, packageVersion, ROOT } from "../build.mjs";

export const RELEASES = join(ROOT, "releases");
export const MUTABLE = new Set(["revoked.json"]);
export const SIZE_GROWTH_LIMIT = 0.1;
const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

export function compareVersions(a, b) {
  const pa = a.split(".").map(Number);
  const pb = b.split(".").map(Number);
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return pa[i] - pb[i];
  return 0;
}

export function readManifest(dir = RELEASES) {
  return JSON.parse(readFileSync(join(dir, "manifest.json"), "utf8"));
}

export function readRevoked(dir = RELEASES) {
  return JSON.parse(readFileSync(join(dir, "revoked.json"), "utf8"));
}

export function newest(manifest) {
  return manifest.versions.at(-1);
}

// the release directory agrees with itself: files match their manifest entry,
// nothing is unlisted, versions only go up, revocations name real versions
export function checkConsistency(dir = RELEASES) {
  const problems = [];
  const manifest = readManifest(dir);
  if (!Array.isArray(manifest.versions)) return ["manifest.json has no versions array"];
  const listed = new Set();
  let previous;
  for (const entry of manifest.versions) {
    const { version } = entry;
    if (!SEMVER.test(version)) problems.push(`manifest version ${version} is not x.y.z`);
    if (previous && compareVersions(version, previous) <= 0)
      problems.push(`manifest version ${version} does not follow ${previous}`);
    previous = version;
    listed.add(version);
    if (entry.file !== `${version}/sdk.js`)
      problems.push(`${version}: file is ${entry.file}, expected ${version}/sdk.js`);
    const path = join(dir, version, "sdk.js");
    if (!existsSync(path)) {
      problems.push(`${version}: ${relative(ROOT, path)} is missing`);
      continue;
    }
    const d = describe(readFileSync(path));
    if (d.sha384 !== entry.sha384) problems.push(`${version}: sdk.js sha384 differs from manifest`);
    if (d.integrity !== entry.integrity)
      problems.push(`${version}: sdk.js integrity differs from manifest`);
    if (d.size !== entry.bytes) problems.push(`${version}: sdk.js size differs from manifest`);
    if (d.gzipSize !== entry.gzipBytes)
      problems.push(`${version}: sdk.js gzip size differs from manifest`);
    const extra = readdirSync(join(dir, version)).filter((f) => f !== "sdk.js");
    if (extra.length > 0) problems.push(`${version}: unexpected files ${extra.join(", ")}`);
  }
  for (const name of readdirSync(dir)) {
    if (name === "manifest.json" || MUTABLE.has(name)) continue;
    if (!listed.has(name)) problems.push(`releases/${name} is not in the manifest`);
  }
  const revoked = readRevoked(dir);
  if (!Array.isArray(revoked.revoked)) problems.push("revoked.json has no revoked array");
  else
    for (const r of revoked.revoked) {
      if (!listed.has(r.version)) problems.push(`revoked version ${r.version} was never released`);
      if (typeof r.reason !== "string" || r.reason === "")
        problems.push(`revoked version ${r.version} has no reason`);
    }
  return problems;
}

function git(repo, args, encoding) {
  return execFileSync("git", ["-C", repo, ...args], {
    encoding,
    stdio: ["ignore", "pipe", "pipe"],
    maxBuffer: 1 << 26,
  });
}

// nothing that existed under releases/ at the base ref was changed or removed.
// new files and new manifest entries at the end are allowed.
export function checkAgainstBase(repo, releasesPath, ref) {
  const problems = [];
  git(repo, ["rev-parse", "--verify", `${ref}^{commit}`], "utf8");
  const listing = git(repo, ["ls-tree", "-r", "--name-only", ref, "--", releasesPath], "utf8")
    .split("\n")
    .filter((l) => l !== "");
  for (const path of listing) {
    const name = path.slice(releasesPath.length + 1);
    if (MUTABLE.has(name)) continue;
    const before = git(repo, ["show", `${ref}:${path}`]);
    const local = join(repo, path);
    if (!existsSync(local)) {
      problems.push(`${path} was removed`);
      continue;
    }
    const now = readFileSync(local);
    if (name === "manifest.json") {
      const old = JSON.parse(before.toString("utf8")).versions;
      const cur = JSON.parse(now.toString("utf8")).versions;
      old.forEach((entry, i) => {
        if (JSON.stringify(entry) !== JSON.stringify(cur[i]))
          problems.push(`manifest entry ${entry.version} was changed, moved or removed`);
      });
    } else if (!before.equals(now)) {
      problems.push(`${path} was changed`);
    }
  }
  return problems;
}

// a rebuild of the newest release from the current source gives the same bytes
export async function checkReproducible(dir = RELEASES) {
  const entry = newest(readManifest(dir));
  if (!entry) return ["no release exists yet"];
  const pkg = packageVersion();
  if (pkg !== entry.version)
    return [
      `package.json version ${pkg} is not the newest release ${entry.version}; ` +
        `run pnpm --filter tracker release ${pkg}`,
    ];
  const built = await build(entry.version);
  const committed = readFileSync(join(dir, entry.version, "sdk.js"));
  if (!built.bytes.equals(committed))
    return [
      `rebuild of ${entry.version} differs from the committed file ` +
        `(built sha384 ${built.sha384}, committed ${entry.sha384}); ` +
        "a source change needs a new version and a release",
    ];
  return [];
}

// the current source may not grow gzip size by more than 10% over the newest
// release unless it is released as a new version
export async function checkSize(dir = RELEASES) {
  const entry = newest(readManifest(dir));
  if (!entry) return [];
  const built = await build(packageVersion());
  const limit = Math.floor(entry.gzipBytes * (1 + SIZE_GROWTH_LIMIT));
  if (packageVersion() === entry.version && built.gzipSize > limit)
    return [
      `gzip size ${built.gzipSize} exceeds ${limit} (newest release ${entry.gzipBytes} + 10%)`,
    ];
  return [];
}

export function repoRoot() {
  return git(ROOT, ["rev-parse", "--show-toplevel"], "utf8").trim();
}

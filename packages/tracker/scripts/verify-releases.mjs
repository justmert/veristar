// pnpm --filter tracker verify-releases [--base <git ref>]
// fails when a committed release file or manifest entry changed, when the
// release directory is inconsistent, when a rebuild of the newest version is
// not byte-identical, or when the source grew more than 10% gzip without a new
// version. ci passes the base ref of the change; locally the default is HEAD.

import { execFileSync } from "node:child_process";
import { relative } from "node:path";
import { parseArgs } from "node:util";
import {
  checkAgainstBase,
  checkConsistency,
  checkReproducible,
  checkSize,
  RELEASES,
  repoRoot,
} from "./lib.mjs";

const { values } = parseArgs({ options: { base: { type: "string" } } });
const repo = repoRoot();
const base = values.base ?? "HEAD";

const problems = [...checkConsistency()];
let hasBase = true;
try {
  execFileSync("git", ["-C", repo, "rev-parse", "--verify", `${base}^{commit}`], {
    stdio: "ignore",
  });
} catch {
  hasBase = false;
}
// an explicit base that does not resolve is an error; a repository without any
// commit yet has nothing to compare against
if (!hasBase && values.base) problems.push(`base ref ${values.base} does not resolve`);
if (hasBase) problems.push(...checkAgainstBase(repo, relative(repo, RELEASES), base));
problems.push(...(await checkSize()));
problems.push(...(await checkReproducible()));

if (problems.length > 0) {
  console.error(problems.map((p) => `release check failed: ${p}`).join("\n"));
  process.exit(1);
}
console.log(`releases verified against ${hasBase ? base : "no base commit"}`);

// runs inside the browser container (tests/fixtures/browserbox) through
// docker exec: starts playwright's chromium as a plain process, with no
// debugger attached, for a spec that needs chromium as a visitor runs it
// (prerendering is off for a page under devtools). the arguments are passed
// on; the profile is a fresh directory removed afterwards. closing stdin
// stops chromium, which is how the spec on the host ends it: a killed docker
// exec client would leave the process running in the container.
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";

const { chromium } = createRequire(import.meta.url)("/pw/index.js");
const dir = mkdtempSync(join(tmpdir(), "sa-undebugged-"));
// --no-sandbox as playwright launches it: chromium's sandbox needs user
// namespaces, which docker's default seccomp profile does not grant
const proc = spawn(
  chromium.executablePath(),
  [...process.argv.slice(2), `--user-data-dir=${dir}`, "--no-sandbox"],
  { stdio: "ignore" },
);
let stopping = false;
const exited = new Promise((resolve) => proc.once("exit", resolve));
const clean = () => rmSync(dir, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
process.stdin.resume();
process.stdin.on("end", async () => {
  stopping = true;
  proc.kill();
  await exited;
  clean();
  process.exit(0);
});
// chromium ending on its own is a failure the spec sees as a missing visit
exited.then(() => {
  if (stopping) return;
  clean();
  process.exit(1);
});

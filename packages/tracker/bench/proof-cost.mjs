// measures how long sdk.js takes to solve a proof-of-work challenge in
// chromium, firefox and webkit, unthrottled and with the cpu slowed 4x and 6x.
//
// playwright can throttle the cpu only in chromium (cdp). to throttle all three
// engines the same way, each engine runs in a child process inside a transient
// systemd scope with a cpu quota: 25% of one cpu is a 4x slowdown and 16.7% is
// 6x. chromium is also measured with cdp throttling, to compare the two.
//
// usage: node bench/proof-cost.mjs [--runs 31] [--out bench/results/<date>.json]

import { spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { chromium, firefox, webkit } from "@playwright/test";
import * as esbuild from "esbuild";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const ENGINES = { chromium, firefox, webkit };
const BITS = [10, 12, 14];
const SLOWDOWNS = [1, 4, 6];
const BATCHES = [1, 16, 64, 256];
const SITE = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b";

const { values } = parseArgs({
  options: {
    runs: { type: "string", default: "31" },
    out: { type: "string" },
    worker: { type: "string" },
    slowdown: { type: "string" },
  },
});
const RUNS = Number(values.runs);

function stats(samples) {
  const sorted = [...samples].sort((a, b) => a - b);
  const at = (q) => sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))];
  const round = (n) => Math.round(n * 10) / 10;
  return {
    n: sorted.length,
    median: round(at(0.5)),
    p90: round(at(0.9)),
    max: round(sorted[sorted.length - 1]),
  };
}

async function servePage() {
  const bundle = await esbuild.build({
    absWorkingDir: ROOT,
    stdin: {
      contents: `import { solve } from "./src/proof.ts"; window.solveProof = solve;`,
      resolveDir: ROOT,
      loader: "ts",
    },
    bundle: true,
    format: "iife",
    target: ["es2020"],
    write: false,
  });
  const js = bundle.outputFiles[0].text;
  const html = `<!doctype html><meta charset="utf-8"><title>proof bench</title><script>${js}</script>`;
  const server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    res.end(html);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  // 127.0.0.1 is a secure context in all three engines, which webcrypto needs
  return { server, url: `http://127.0.0.1:${server.address().port}/` };
}

async function measure(page, bits, batch, runs) {
  const samples = [];
  const hashes = [];
  for (let i = 0; i < runs; i++) {
    const challenge = {
      w: SITE,
      iat: Math.floor(Date.now() / 1000),
      salt: randomBytes(16).toString("hex"),
      bits,
      tag: randomBytes(32).toString("hex"),
    };
    const r = await page.evaluate(
      async ({ challenge, batch }) => {
        const t0 = performance.now();
        const proof = await window.solveProof(crypto.subtle, challenge, batch);
        return { ms: performance.now() - t0, nonce: proof.nonce };
      },
      { challenge, batch },
    );
    samples.push(r.ms);
    hashes.push(r.nonce + 1);
  }
  const s = stats(samples);
  const totalMs = samples.reduce((a, b) => a + b, 0);
  const totalHashes = hashes.reduce((a, b) => a + b, 0);
  return { ...s, hashesPerMs: Math.round(totalHashes / totalMs) };
}

// runs inside the throttled scope: one engine, one slowdown
async function worker(engineName, slowdown) {
  const { server, url } = await servePage();
  const browser = await ENGINES[engineName].launch({ headless: true });
  const version = browser.version();
  const page = await browser.newPage();
  await page.goto(url);
  const rows = [];
  // warm up the jit and the crypto thread before measuring
  await measure(page, 10, 64, 5);
  for (const bits of BITS) {
    rows.push({
      method: "cgroup",
      slowdown,
      bits,
      batch: 64,
      ...(await measure(page, bits, 64, RUNS)),
    });
  }
  for (const batch of BATCHES) {
    rows.push({
      method: "cgroup",
      slowdown,
      bits: 12,
      batch,
      sweep: true,
      ...(await measure(page, 12, batch, RUNS)),
    });
  }
  if (engineName === "chromium" && slowdown === 1) {
    const cdp = await page.context().newCDPSession(page);
    for (const rate of [4, 6]) {
      await cdp.send("Emulation.setCPUThrottlingRate", { rate });
      for (const bits of BITS) {
        rows.push({
          method: "cdp",
          slowdown: rate,
          bits,
          batch: 64,
          ...(await measure(page, bits, 64, RUNS)),
        });
      }
    }
    await cdp.send("Emulation.setCPUThrottlingRate", { rate: 1 });
  }
  await browser.close();
  server.close();
  process.stdout.write(`${JSON.stringify({ engine: engineName, version, rows })}\n`);
}

function runWorker(engine, slowdown) {
  const self = fileURLToPath(import.meta.url);
  const args = [self, "--worker", engine, "--slowdown", String(slowdown), "--runs", String(RUNS)];
  // a 10 ms quota period keeps the throttling smooth at the time scale measured
  const quota = `${Math.round(100 / slowdown)}%`;
  const cmd =
    slowdown === 1
      ? [process.execPath, args]
      : [
          "systemd-run",
          [
            "--user",
            "--scope",
            "--quiet",
            "-p",
            `CPUQuota=${quota}`,
            "-p",
            "CPUQuotaPeriodSec=10ms",
            process.execPath,
            ...args,
          ],
        ];
  const res = spawnSync(cmd[0], cmd[1], { encoding: "utf8", maxBuffer: 1 << 24 });
  if (res.status !== 0) throw new Error(`${engine} x${slowdown} failed: ${res.stderr}`);
  return JSON.parse(res.stdout.trim().split("\n").pop());
}

if (values.worker) {
  await worker(values.worker, Number(values.slowdown));
} else {
  const results = { date: new Date().toISOString(), runs: RUNS, cpu: cpuModel(), engines: {} };
  for (const engine of Object.keys(ENGINES)) {
    for (const slowdown of SLOWDOWNS) {
      const r = runWorker(engine, slowdown);
      results.engines[engine] ??= { version: r.version, rows: [] };
      results.engines[engine].rows.push(...r.rows);
      for (const row of r.rows) console.log(engine, JSON.stringify(row));
    }
  }
  if (values.out) {
    mkdirSync(dirname(values.out), { recursive: true });
    writeFileSync(values.out, `${JSON.stringify(results, null, 2)}\n`);
  }
}

function cpuModel() {
  const res = spawnSync("sh", ["-c", "grep -m1 'model name' /proc/cpuinfo | cut -d: -f2"], {
    encoding: "utf8",
  });
  return res.stdout.trim();
}
